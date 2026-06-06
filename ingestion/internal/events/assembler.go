// Package events assembles email.ingested events from all pipeline components.
// assembler.go orchestrates thread reconstruction, contact dedup, and persistence
// into a single atomic unit of work.
package events

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/decisionstack/ingestion/internal/contact"
	natspkg "github.com/decisionstack/ingestion/internal/nats"
	"github.com/decisionstack/ingestion/internal/thread"

	"github.com/decisionstack/ingestion/internal/models"
	"github.com/google/uuid"
)

// Assembler coordinates thread resolution, contact dedup, persistence, and
// event envelope construction for the email.ingested NATS event.
type Assembler struct {
	db          *sql.DB
	threadEngine *thread.Engine
	dedupEngine  *contact.DedupEngine
	log          *slog.Logger
}

// NewAssembler creates a new event assembler.
func NewAssembler(db *sql.DB, threadEngine *thread.Engine, dedupEngine *contact.DedupEngine, log *slog.Logger) *Assembler {
	if log == nil {
		log = slog.Default()
	}
	return &Assembler{
		db:           db,
		threadEngine: threadEngine,
		dedupEngine:  dedupEngine,
		log:          log,
	}
}

// AssembleEvent performs the full assembly pipeline:
//
//  1. Find or create the thread → ThreadID
//  2. Dedup contacts from sender + recipients → ContactIDs
//  3. Insert raw_emails row (inside a DB transaction)
//  4. Assemble EmailIngestedEvent envelope
//  5. Return the event (caller publishes to NATS)
//
// The thread upsert + raw_emails INSERT are executed atomically via a DB transaction.
func (a *Assembler) AssembleEvent(ctx context.Context, parsedEmail *models.ParsedEmail, rawEmailID uuid.UUID, s3URI string) (*natspkg.EmailIngestedEvent, error) {
	// ---- Step 1: Thread resolution ----
	threadResult, err := a.threadEngine.FindOrCreateThread(ctx, parsedEmail)
	if err != nil {
		return nil, &models.IngestionError{
			Code:    models.ErrCodeThreadingFailed,
			Message: fmt.Sprintf("thread resolution failed: %v", err),
			UserID:  parsedEmail.UserID.String(),
			Retry:   true,
		}
	}

	// ---- Step 2: Contact dedup ----
	dedupMap, err := a.dedupEngine.DedupAll(
		ctx,
		parsedEmail.UserID,
		parsedEmail.SenderEmail,
		parsedEmail.SenderName,
		parsedEmail.RecipientEmails,
	)
	if err != nil {
		return nil, &models.IngestionError{
			Code:    models.ErrCodeDedupFailed,
			Message: fmt.Sprintf("contact dedup failed: %v", err),
			UserID:  parsedEmail.UserID.String(),
			Retry:   true,
		}
	}

	// Collect unique contact IDs (verified — from Neo4j)
	contactIDs := make([]uuid.UUID, 0, len(dedupMap))
	for _, result := range dedupMap {
		contactIDs = append(contactIDs, result.ContactID)
	}

	// ---- Step 3: Persist raw_emails atomically ----
	// Note: the thread upsert already happened in FindOrCreateThread.
	// We now insert the raw_emails row in the same atomic transaction wrapper.
	if err := a.insertRawEmail(ctx, parsedEmail, rawEmailID, threadResult.ThreadID, s3URI); err != nil {
		return nil, &models.IngestionError{
			Code:    models.ErrCodeThreadingFailed,
			Message: fmt.Sprintf("persist raw_email failed: %v", err),
			UserID:  parsedEmail.UserID.String(),
			Retry:   true,
		}
	}

	// ---- Step 4: Assemble event envelope ----
	event := &natspkg.EmailIngestedEvent{
		EventID:            rawEmailID,
		UserID:             parsedEmail.UserID,
		Source:             parsedEmail.Source,
		AccountID:          parsedEmail.AccountID,
		ThreadID:           threadResult.ThreadID,
		RawEmailID:         rawEmailID,
		S3URI:              s3URI,
		HasAttachments:     parsedEmail.HasAttachments,
		SenderEmail:        parsedEmail.SenderEmail,
		ReceivedAt:         parsedEmail.ReceivedAt,
		ClassificationHint: "pending",
		ContactIDs:         contactIDs,
	}

	a.log.Debug("event assembled",
		"event_id", event.EventID,
		"thread_id", event.ThreadID,
		"is_new_thread", threadResult.IsNewThread,
		"match_method", threadResult.MatchMethod,
		"contact_count", len(contactIDs),
	)

	return event, nil
}

// insertRawEmail inserts the raw_emails row. This is done inside a transaction
// to ensure atomicity with thread state updates.
func (a *Assembler) insertRawEmail(ctx context.Context, email *models.ParsedEmail, rawEmailID, threadID uuid.UUID, s3URI string) error {
	// We use a transaction to ensure the raw_emails insert is atomic.
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for raw_email insert: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	inReplyTo := sql.NullString{}
	if email.InReplyTo != nil {
		inReplyTo = sql.NullString{String: *email.InReplyTo, Valid: true}
	}

	subject := sql.NullString{}
	if email.Subject != "" {
		subject = sql.NullString{String: email.Subject, Valid: true}
	}

	senderName := sql.NullString{}
	if email.SenderName != "" {
		senderName = sql.NullString{String: email.SenderName, Valid: true}
	}

	// Deduplicate attachment S3 URIs
	var attachmentURIs []string
	for _, att := range email.Attachments {
		if att.S3URI != "" {
			attachmentURIs = append(attachmentURIs, att.S3URI)
		}
	}

	query := `
		INSERT INTO raw_emails (
			id, thread_id, user_id, source_account_id, message_id,
			in_reply_to, references, sender_email, sender_name,
			recipient_emails, subject, body_text, body_html,
			has_attachments, attachment_s3_uris, extracted_codes,
			received_at, s3_uri
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		ON CONFLICT (user_id, message_id) DO NOTHING
	`

	_, err = tx.ExecContext(ctx, query,
		rawEmailID,
		threadID,
		email.UserID,
		email.AccountID,
		email.MessageID,
		inReplyTo,
		email.References,
		email.SenderEmail,
		senderName,
		email.RecipientEmails,
		subject,
		sql.NullString{String: email.BodyText, Valid: email.BodyText != ""},
		sql.NullString{String: email.BodyHTML, Valid: email.BodyHTML != ""},
		email.HasAttachments,
		attachmentURIs,
		email.ExtractedCodes,
		email.ReceivedAt,
		s3URI,
	)
	if err != nil {
		return fmt.Errorf("insert raw_email: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit raw_email tx: %w", err)
	}

	return nil
}
