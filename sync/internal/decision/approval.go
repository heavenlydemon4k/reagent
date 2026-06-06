package decision

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/nats-io/nats.go"
)

// NATSSubjectEmailSend is the NATS subject for email send jobs.
const NATSSubjectEmailSend = "email.send"

// ApprovalFlow handles draft approval and send queueing.
type ApprovalFlow struct {
	draftStore    *DraftStore
	cardStore     *CardStore
	natsConn      NatsPublisher
	log           *slog.Logger
}

// NatsPublisher is the interface for NATS publishing.
type NatsPublisher interface {
	Publish(subject string, data []byte) error
}

// SendJobPayload is the message published to NATS for email sending.
type SendJobPayload struct {
	DraftID   uuid.UUID `json:"draft_id"`
	UserID    uuid.UUID `json:"user_id"`
	ThreadID  uuid.UUID `json:"thread_id"`
	DraftBody string    `json:"draft_body"`
	Subject   string    `json:"subject"`
	InReplyTo *string   `json:"in_reply_to,omitempty"`
	References []string `json:"references,omitempty"`
}

// NewApprovalFlow creates a new approval flow handler.
func NewApprovalFlow(draftStore *DraftStore, cardStore *CardStore, natsConn NatsPublisher, log *slog.Logger) *ApprovalFlow {
	return &ApprovalFlow{
		draftStore:    draftStore,
		cardStore:     cardStore,
		natsConn:      natsConn,
		log:           log,
	}
}

// Approve marks a draft as user-approved and publishes a send job to NATS.
// This is an atomic operation: approval is recorded in PostgreSQL and the send
// job is queued. If NATS publish fails, the transaction is rolled back.
func (a *ApprovalFlow) Approve(ctx context.Context, draftID uuid.UUID, userID uuid.UUID) error {
	// Start a transaction — ensures approval + logging are atomic
	tx, err := a.draftStore.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin approval transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Verify and lock the draft, mark as approved
	if err := a.draftStore.ApproveDraftTx(ctx, tx, draftID, userID); err != nil {
		return fmt.Errorf("approve draft: %w", err)
	}

	// 2. Fetch the full draft for the send job
	draft, err := a.draftStore.GetDraft(ctx, draftID)
	if err != nil {
		return fmt.Errorf("fetch draft after approval: %w", err)
	}

	// Double-check ownership
	if draft.UserID != userID {
		return ErrCardOwnership{CardID: draft.CardID, UserID: userID}
	}

	// 3. Update card state to "approved"
	if err := a.cardStore.UpdateCardStateTx(ctx, tx, draft.CardID, "approved"); err != nil {
		return fmt.Errorf("update card state to approved: %w", err)
	}

	// 4. Build and publish send job to NATS
	subject := ""
	if draft.SubjectLine != nil {
		subject = *draft.SubjectLine
	}

	sendJob := SendJobPayload{
		DraftID:    draftID,
		UserID:     userID,
		ThreadID:   draft.ThreadID,
		DraftBody:  draft.DraftBody,
		Subject:    subject,
		InReplyTo:  draft.InReplyTo,
		References: draft.References,
	}

	jobBytes, err := json.Marshal(sendJob)
	if err != nil {
		return fmt.Errorf("marshal send job: %w", err)
	}

	// Publish to NATS — this is best-effort; if it fails we rollback
	if err := a.natsConn.Publish(NATSSubjectEmailSend, jobBytes); err != nil {
		return fmt.Errorf("publish send job to nats: %w", err)
	}

	// 5. Log the decision
	logDetails := fmt.Sprintf(`{"action":"approve","draft_id":"%s","card_id":"%s"}`, draftID, draft.CardID)
	if err := a.draftStore.LogDecisionTx(ctx, tx, userID, draft.CardID, "approve", logDetails); err != nil {
		a.log.Warn("failed to log approval decision", "error", err, "draft_id", draftID)
		// Non-fatal: don't fail the approval if logging fails
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit approval transaction: %w", err)
	}

	a.log.Info("draft approved and send job queued",
		"draft_id", draftID,
		"card_id", draft.CardID,
		"user_id", userID,
	)

	return nil
}

// OnSendComplete handles the callback from Ingestion Mesh after a draft is sent.
// It updates the draft and card records and notifies the user.
func (a *ApprovalFlow) OnSendComplete(ctx context.Context, draftID uuid.UUID, messageID string) error {
	// Start a transaction
	tx, err := a.draftStore.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin send-complete transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Update draft with sent_at and message_id
	if err := a.draftStore.MarkDraftSentTx(ctx, tx, draftID, messageID); err != nil {
		return fmt.Errorf("mark draft sent: %w", err)
	}

	// 2. Fetch the card ID from the draft
	draft, err := a.draftStore.GetDraft(ctx, draftID)
	if err != nil {
		return fmt.Errorf("fetch draft for card update: %w", err)
	}

	// 3. Update card state to 'sent'
	if err := a.cardStore.MarkCardSentTx(ctx, tx, draft.CardID); err != nil {
		return fmt.Errorf("mark card sent: %w", err)
	}

	// 4. Log the send
	logDetails := fmt.Sprintf(`{"action":"send","draft_id":"%s","card_id":"%s","message_id":"%s"}`, draftID, draft.CardID, messageID)
	if err := a.draftStore.LogDecisionTx(ctx, tx, draft.UserID, draft.CardID, "send", logDetails); err != nil {
		a.log.Warn("failed to log send completion", "error", err, "draft_id", draftID)
	}

	// Commit
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit send-complete transaction: %w", err)
	}

	a.log.Info("send completed",
		"draft_id", draftID,
		"card_id", draft.CardID,
		"message_id", messageID,
	)

	return nil
}

// ---------------------------------------------------------------------------
// Send execution (direct gRPC call to Ingestion Mesh)
// ---------------------------------------------------------------------------

// IngestionMeshClient is the interface for calling the Ingestion Mesh.
type IngestionMeshClient interface {
	SendEmail(ctx context.Context, draftID uuid.UUID, userID uuid.UUID, draftBody, subject string, inReplyTo *string, references []string) (sentAt time.Time, messageID string, err error)
}

// ExecuteSend performs an immediate send by calling Ingestion Mesh gRPC.
// This bypasses the NATS queue for urgent sends.
func (a *ApprovalFlow) ExecuteSend(ctx context.Context, meshClient IngestionMeshClient, draftID uuid.UUID, userID uuid.UUID) (*SendResult, error) {
	// Verify draft is approved
	draft, err := a.draftStore.GetDraftOwnedBy(ctx, draftID, userID)
	if err != nil {
		return nil, fmt.Errorf("fetch draft for send: %w", err)
	}

	if !draft.UserApproved {
		return nil, ErrNotApproved{DraftID: draftID}
	}
	if draft.SentAt != nil {
		return nil, ErrAlreadySent{DraftID: draftID}
	}

	subject := ""
	if draft.SubjectLine != nil {
		subject = *draft.SubjectLine
	}

	// Call Ingestion Mesh
	sentAt, messageID, err := meshClient.SendEmail(
		ctx, draft.ID, userID, draft.DraftBody,
		subject, draft.InReplyTo, draft.References,
	)
	if err != nil {
		return nil, fmt.Errorf("ingestion mesh send: %w", err)
	}

	// Record completion
	if err := a.OnSendComplete(ctx, draftID, messageID); err != nil {
		return nil, fmt.Errorf("record send completion: %w", err)
	}

	return &SendResult{
		SentAt:    sentAt,
		MessageID: messageID,
	}, nil
}

// SendResult holds the result of a direct send operation.
type SendResult struct {
	SentAt    time.Time `json:"sent_at"`
	MessageID string    `json:"message_id"`
}

// ErrNotApproved is returned when attempting to send a non-approved draft.
type ErrNotApproved struct{ DraftID uuid.UUID }

func (e ErrNotApproved) Error() string { return fmt.Sprintf("draft %s not approved", e.DraftID) }

// ---------------------------------------------------------------------------
// NATS JetStream wrapper (convenience)
// ---------------------------------------------------------------------------

// JetStreamPublisher wraps a NATS JetStream context for publishing.
type JetStreamPublisher struct {
	js nats.JetStreamContext
}

// NewJetStreamPublisher creates a publisher from a JetStream context.
func NewJetStreamPublisher(js nats.JetStreamContext) *JetStreamPublisher {
	return &JetStreamPublisher{js: js}
}

// Publish publishes a message to the given subject.
func (p *JetStreamPublisher) Publish(subject string, data []byte) error {
	_, err := p.js.Publish(subject, data)
	return err
}

// CoreNatsPublisher wraps a core NATS connection for publishing.
type CoreNatsPublisher struct {
	conn *nats.Conn
}

// NewCoreNatsPublisher creates a publisher from a core NATS connection.
func NewCoreNatsPublisher(conn *nats.Conn) *CoreNatsPublisher {
	return &CoreNatsPublisher{conn: conn}
}

// Publish publishes a message to the given subject.
func (p *CoreNatsPublisher) Publish(subject string, data []byte) error {
	return p.conn.Publish(subject, data)
}
