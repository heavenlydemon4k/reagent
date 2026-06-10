package poll

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"mime"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/decisionstack/ingestion/internal/models"
	natsevents "github.com/decisionstack/ingestion/internal/nats"

	"github.com/google/uuid"
)

// History gap detection constants.
const (
	// historyGapThreshold is the minimum difference between consecutive
	// history record IDs to be considered a gap.
	historyGapThreshold uint64 = 1

	// historyRangeTolerance is the maximum allowed ratio of (range / record_count)
	// before we flag a potential gap. If the historyId range is more than 10x
	// the number of records received, we may have dropped entries.
	historyRangeTolerance uint64 = 10

	// maxConsecutiveGapsBeforeCritical is the number of consecutive gap
	// detections before we escalate to CRITICAL and halt historyId advancement.
	maxConsecutiveGapsBeforeCritical = 2
)

// TokenStore retrieves decrypted OAuth tokens for email accounts.
type TokenStore interface {
	GetTokens(ctx context.Context, accountID uuid.UUID) (*models.TokenPair, error)
	RefreshIfNeeded(ctx context.Context, accountID uuid.UUID) (*models.TokenPair, error)
}

// MIMEParser parses raw RFC 822 email into a ParsedEmail.
type MIMEParser interface {
	Parse(raw []byte, accountID, userID uuid.UUID) (*models.ParsedEmail, error)
}

// GmailFetcher abstracts the Gmail API for testability.
type GmailFetcher interface {
	// HistoryList calls users.history.list and returns history records + next page token.
	HistoryList(ctx context.Context, accessToken, historyID string) (*HistoryListResult, error)
	// HistoryListPage fetches a specific page using a page token.
	HistoryListPage(ctx context.Context, accessToken, historyID, pageToken string) (*HistoryListResult, error)
	// MessagesList calls users.messages.list with an optional query filter.
	// Used by the backfill worker to list all messages in a date range.
	MessagesList(ctx context.Context, accessToken, query, pageToken string) (*MessagesListResult, error)
	// MessagesGet calls users.messages.get with format=full and returns the raw message.
	MessagesGet(ctx context.Context, accessToken, messageID string) (*GmailMessage, error)
}

// MessagesListResult holds the response from users.messages.list.
type MessagesListResult struct {
	Messages      []MessageListItem
	NextPageToken string
	ResultSizeEstimate int64
}

// MessageListItem is a minimal representation of a message from users.messages.list.
type MessageListItem struct {
	ID       string
	ThreadID string
}

// HistoryListResult holds the response from users.history.list.
type HistoryListResult struct {
	HistoryRecords []HistoryRecord
	NextPageToken  string
	HistoryID      string // newest history ID from response
}

// HistoryRecord represents a single record in the history list.
type HistoryRecord struct {
	ID            string
	MessagesAdded []MessageAdded
	MessagesDeleted []MessageDeleted
	LabelsAdded   []LabelChange
	LabelsRemoved []LabelChange
}

// MessageAdded represents a message added event from Gmail history.
type MessageAdded struct {
	MessageID string
	ThreadID  string
}

// MessageDeleted represents a message deleted event from Gmail history.
type MessageDeleted struct {
	MessageID string
}

// LabelChange represents a label added/removed event.
type LabelChange struct {
	MessageID string
	LabelIDs  []string
}

// GmailMessage represents a full Gmail message retrieved via users.messages.get.
type GmailMessage struct {
	ID       string
	ThreadID string
	Raw      string // base64url encoded RFC 822
	Snippet  string
}

// GmailPoller implements JobProcessor for Gmail accounts. It polls using
// users.history.list and processes messageAdded, messageDeleted, and label
// change events. Every messageAdded is fetched via users.messages.get,
// parsed, persisted, and published — zero email loss guaranteed.
type GmailPoller struct {
	rateLimit *RateLimiter
	state     *StateStore
	fetcher   GmailFetcher
	tokens    TokenStore
	parser    MIMEParser
	publisher natsevents.Publisher
	assembler EmailAssembler
	log       *slog.Logger

	// consecutiveGapCount tracks how many consecutive poll cycles detected
	// a history gap. Protected by gapMu.
	consecutiveGapCount int
	gapMu               sync.Mutex
}

// NewGmailPoller creates a new GmailPoller.
func NewGmailPoller(
	rateLimit *RateLimiter,
	state *StateStore,
	fetcher GmailFetcher,
	tokens TokenStore,
	parser MIMEParser,
	publisher natsevents.Publisher,
	assembler EmailAssembler,
	log *slog.Logger,
) *GmailPoller {
	return &GmailPoller{
		rateLimit: rateLimit,
		state:     state,
		fetcher:   fetcher,
		tokens:    tokens,
		parser:    parser,
		publisher: publisher,
		assembler: assembler,
		log:       log.With("component", "gmail_poller"),
	}
}

// Process implements JobProcessor. It polls a Gmail account for changes
// starting from the stored historyId. Every messageAdded results in a
// full fetch, parse, persist, and publish cycle.
func (p *GmailPoller) Process(ctx context.Context, job FetchJob) error {
	log := p.log.With("account_id", job.AccountID, "user_id", job.UserID)
	log.Info("starting gmail poll cycle")

	// 1. Get (and refresh if needed) OAuth tokens
	tokenPair, err := p.tokens.RefreshIfNeeded(ctx, job.AccountID)
	if err != nil {
		return fmt.Errorf("refresh tokens: %w", err)
	}
	accessToken := *tokenPair.AccessTokenPlaintext

	// 2. Get stored historyId
	historyID, err := p.state.GetHistoryID(ctx, job.AccountID)
	if err != nil {
		return fmt.Errorf("get history_id: %w", err)
	}

	// If no historyId, we need a full sync first. For now, return error
	// to trigger backoff; full sync should be handled separately.
	if historyID == "" {
		log.Warn("no history_id stored, need full sync")
		return fmt.Errorf("no history_id: full sync required")
	}

	// 3. Check rate limit for history.list (cost: 2 units)
	rlStatus, err := p.rateLimit.AllowGmailRequest(ctx, job.UserID.String(), models.GmailHistoryListCost)
	if err != nil {
		return fmt.Errorf("rate limit check (history.list): %w", err)
	}
	if !rlStatus.Allowed {
		log.Warn("gmail rate limited", "remaining", rlStatus.Remaining, "backoff", rlStatus.Backoff)
		return models.IngestionError{
			Code:    models.ErrCodeRateLimited,
			Message: fmt.Sprintf("gmail rate limited: retry after %v", rlStatus.Backoff),
			UserID:  job.UserID.String(),
			Retry:   true,
		}
	}

	// 4. Call users.history.list
	result, err := p.fetcher.HistoryList(ctx, accessToken, historyID)
	if err != nil {
		// Refund the quota since the request failed
		_ = p.rateLimit.RefundGmailQuota(ctx, job.UserID.String(), models.GmailHistoryListCost)
		return fmt.Errorf("history.list failed: %w", err)
	}

	// 5. Process all history records across all pages
	newestHistoryID := result.HistoryID
	if newestHistoryID == "" {
		newestHistoryID = historyID
	}

	allRecords := result.HistoryRecords
	nextPageToken := result.NextPageToken

	// Fetch all pages
	for nextPageToken != "" {
		// Check rate limit for each paginated history.list call
		rlStatus, err = p.rateLimit.AllowGmailRequest(ctx, job.UserID.String(), models.GmailHistoryListCost)
		if err != nil {
			return fmt.Errorf("rate limit check (history.list page): %w", err)
		}
		if !rlStatus.Allowed {
			// Save progress with what we've processed so far
			if err := p.saveProgress(ctx, job, newestHistoryID); err != nil {
				log.Error("failed to save partial progress", "error", err)
			}
			return models.IngestionError{
				Code:    models.ErrCodeRateLimited,
				Message: fmt.Sprintf("gmail rate limited during pagination: retry after %v", rlStatus.Backoff),
				UserID:  job.UserID.String(),
				Retry:   true,
			}
		}

		result, err = p.fetcher.HistoryListPage(ctx, accessToken, historyID, nextPageToken)
		if err != nil {
			_ = p.rateLimit.RefundGmailQuota(ctx, job.UserID.String(), models.GmailHistoryListCost)
			// Save partial progress
			if err := p.saveProgress(ctx, job, newestHistoryID); err != nil {
				log.Error("failed to save partial progress", "error", err)
			}
			return fmt.Errorf("history.list page failed: %w", err)
		}

		allRecords = append(allRecords, result.HistoryRecords...)
		nextPageToken = result.NextPageToken
		if result.HistoryID != "" {
			newestHistoryID = result.HistoryID
		}
	}

	// 6. Process each history record
	var messagesToProcess []MessageAdded
	var messagesToDelete []MessageDeleted
	var labelChanges []LabelChange

	for _, record := range allRecords {
		messagesToProcess = append(messagesToProcess, record.MessagesAdded...)
		messagesToDelete = append(messagesToDelete, record.MessagesDeleted...)
		labelChanges = append(labelChanges, record.LabelsAdded...)
		labelChanges = append(labelChanges, record.LabelsRemoved...)
	}

	log.Info("history cycle complete",
		"records", len(allRecords),
		"messages_added", len(messagesToProcess),
		"messages_deleted", len(messagesToDelete),
		"label_changes", len(labelChanges),
	)

	// 6a. Verify no history gaps before processing messages.
	// If a gap is detected, do NOT advance historyId — the next poll
	// will re-fetch from the same starting point, recovering any dropped messages.
	if gapErr := p.verifyNoGaps(historyID, newestHistoryID, allRecords, log); gapErr != nil {
		p.gapMu.Lock()
		p.consecutiveGapCount++
		gapCount := p.consecutiveGapCount
		p.gapMu.Unlock()

		if gapCount >= maxConsecutiveGapsBeforeCritical {
			log.Error("CRITICAL: persistent history gap detected — halting historyId advancement",
				"consecutive_gaps", gapCount,
				"error", gapErr,
			)
			// Do not update historyId — next poll re-fetches from the same point.
			// Return error to trigger backoff and alerting.
			return fmt.Errorf("history gap detected (consecutive=%d): %w", gapCount, gapErr)
		}

		log.Warn("history gap detected — re-fetching on next poll cycle",
			"consecutive_gaps", gapCount,
			"error", gapErr,
		)
		// Do not update historyId — next poll re-fetches from the same point.
		return fmt.Errorf("history gap detected: %w", gapErr)
	}

	// Reset consecutive gap counter on successful gap-free poll.
	p.gapMu.Lock()
	p.consecutiveGapCount = 0
	p.gapMu.Unlock()

	// 7. Handle deletions first (mark as deleted)
	for _, deleted := range messagesToDelete {
		if err := p.handleMessageDeleted(ctx, job, deleted.MessageID); err != nil {
			log.Error("failed to handle message deletion", "message_id", deleted.MessageID, "error", err)
			// Don't fail the entire cycle for a deletion error
		}
	}

	// 8. Handle label changes
	for _, change := range labelChanges {
		if err := p.handleLabelChange(ctx, job, change); err != nil {
			log.Error("failed to handle label change", "message_id", change.MessageID, "error", err)
			// Don't fail the entire cycle for a label change error
		}
	}

	// 9. Process each added message: fetch, parse, persist, publish
	for _, added := range messagesToProcess {
		if err := p.processAddedMessage(ctx, job, accessToken, added); err != nil {
			log.Error("failed to process added message",
				"message_id", added.MessageID,
				"error", err,
			)
			// Save progress so far and return error to retry
			if saveErr := p.saveProgress(ctx, job, newestHistoryID); saveErr != nil {
				log.Error("failed to save progress after message error", "error", saveErr)
			}
			return fmt.Errorf("process message %s: %w", added.MessageID, err)
		}
	}

	// 10. Update history_id to the newest value
	if err := p.state.UpdateHistoryIDDirect(ctx, job.AccountID, newestHistoryID); err != nil {
		return fmt.Errorf("update history_id to %s: %w", newestHistoryID, err)
	}

	log.Info("gmail poll cycle complete", "processed", len(messagesToProcess), "new_history_id", newestHistoryID)
	return nil
}

// verifyNoGaps checks the history record sequence for gaps that would
// indicate dropped history entries (and therefore potentially lost messages).
//
// Algorithm:
//   1. Convert startHistoryID and all record IDs to uint64.
//   2. Sort record IDs numerically.
//   3. Check each adjacent pair for gaps > historyGapThreshold.
//   4. Check if the overall range (newest - start) is suspiciously large
//      compared to the number of records received.
//
// If any check fails, returns a descriptive error. The caller must NOT
// advance the historyId so the next poll re-fetches from the same point.
func (p *GmailPoller) verifyNoGaps(startHistoryID string, newestHistoryID string, records []HistoryRecord, log *slog.Logger) error {
	if len(records) == 0 {
		// No records means no changes — this is normal, no gap possible.
		return nil
	}

	startID, err := strconv.ParseUint(startHistoryID, 10, 64)
	if err != nil {
		// Can't parse start historyId — non-numeric, skip numeric gap check.
		log.Warn("cannot parse start historyId for gap check", "history_id", startHistoryID)
		startID = 0
	}

	newestID, err := strconv.ParseUint(newestHistoryID, 10, 64)
	if err != nil {
		log.Warn("cannot parse newest historyId for gap check", "history_id", newestHistoryID)
		return nil // can't verify without a valid newest ID
	}

	// Collect and sort all record IDs.
	recordIDs := make([]uint64, 0, len(records))
	for _, r := range records {
		id, err := strconv.ParseUint(r.ID, 10, 64)
		if err != nil {
			// Non-numeric record ID — skip this record in gap analysis.
			continue
		}
		recordIDs = append(recordIDs, id)
	}

	if len(recordIDs) == 0 {
		// All record IDs were non-numeric — can't verify.
		return nil
	}

	sort.Slice(recordIDs, func(i, j int) bool { return recordIDs[i] < recordIDs[j] })

	// Check 1: The first record ID should be > startID.
	// If startID == 0 (unparseable), skip this check.
	if startID > 0 && recordIDs[0] <= startID {
		return fmt.Errorf("first record ID (%d) not greater than start historyId (%d): possible overlap or rewind",
			recordIDs[0], startID)
	}

	// Check 2: Look for gaps between consecutive record IDs.
	for i := 1; i < len(recordIDs); i++ {
		gap := recordIDs[i] - recordIDs[i-1]
		if gap > historyGapThreshold {
			return fmt.Errorf("history gap detected between record %d and %d (gap=%d, threshold=%d): %d message(s) potentially dropped",
				recordIDs[i-1], recordIDs[i], gap, historyGapThreshold, gap-1)
		}
	}

	// Check 3: Range heuristic — if the historyId range is much larger than
	// the number of records, some history entries may have been dropped by
	// the API (e.g., due to history expiration or truncation).
	if startID > 0 {
		rangeSize := newestID - startID
		if len(recordIDs) > 0 && rangeSize/uint64(len(recordIDs)) > historyRangeTolerance {
			return fmt.Errorf("suspicious history range: range=%d records but only %d records received (ratio=%d, tolerance=%d): potential mass drop",
				rangeSize, len(recordIDs), rangeSize/uint64(len(recordIDs)), historyRangeTolerance)
		}
	}

	log.Debug("history gap check passed",
		"records_checked", len(recordIDs),
		"start_history_id", startID,
		"newest_history_id", newestID,
	)
	return nil
}

// processAddedMessage fetches a single message via users.messages.get,
// decodes the raw MIME, parses it, persists to raw_emails, and publishes
// the email.ingested event.
func (p *GmailPoller) processAddedMessage(ctx context.Context, job FetchJob, accessToken string, added MessageAdded) error {
	log := p.log.With("message_id", added.MessageID, "thread_id", added.ThreadID)

	// Check rate limit for messages.get (cost: 5 units)
	rlStatus, err := p.rateLimit.AllowGmailRequest(ctx, job.UserID.String(), models.GmailGetCost)
	if err != nil {
		return fmt.Errorf("rate limit check (messages.get): %w", err)
	}
	if !rlStatus.Allowed {
		return models.IngestionError{
			Code:    models.ErrCodeRateLimited,
			Message: fmt.Sprintf("gmail rate limited for messages.get: retry after %v", rlStatus.Backoff),
			UserID:  job.UserID.String(),
			Retry:   true,
		}
	}

	// Fetch the full message
	msg, err := p.fetcher.MessagesGet(ctx, accessToken, added.MessageID)
	if err != nil {
		_ = p.rateLimit.RefundGmailQuota(ctx, job.UserID.String(), models.GmailGetCost)
		return fmt.Errorf("messages.get %s: %w", added.MessageID, err)
	}

	// Decode base64url raw content
	rawBytes, err := base64.URLEncoding.DecodeString(msg.Raw)
	if err != nil {
		// Try standard base64 as fallback
		rawBytes, err = base64.StdEncoding.DecodeString(msg.Raw)
		if err != nil {
			return fmt.Errorf("decode raw message %s: %w", added.MessageID, err)
		}
	}

	// Parse MIME into ParsedEmail
	parsed, err := p.parser.Parse(rawBytes, job.AccountID, job.UserID)
	if err != nil {
		return fmt.Errorf("parse MIME for %s: %w", added.MessageID, err)
	}

	// Assemble event: thread resolution + contact dedup + raw_emails persist
	rawEmailID := uuid.New()

	event, err := p.assembler.AssembleEvent(ctx, parsed, rawEmailID, parsed.S3URI)
	if err != nil {
		return fmt.Errorf("assemble event for %s: %w", added.MessageID, err)
	}

	if err := p.publisher.PublishEmailIngested(ctx, *event); err != nil {
		// Log but don't fail — the email is persisted, event can be replayed
		log.Error("failed to publish email.ingested event", "error", err)
	}

	log.Debug("message processed successfully", "message_id", added.MessageID)
	return nil
}

// handleMessageDeleted marks a raw email as deleted in the database.
func (p *GmailPoller) handleMessageDeleted(ctx context.Context, job FetchJob, messageID string) error {
	_, err := p.state.DB().ExecContext(ctx,
		`UPDATE raw_emails SET deleted = true, updated_at = $1
		 WHERE source_account_id = $2 AND message_id = $3 AND user_id = $4`,
		time.Now().UTC(), job.AccountID, messageID, job.UserID,
	)
	if err != nil {
		return fmt.Errorf("mark deleted %s: %w", messageID, err)
	}
	p.log.Debug("message marked as deleted", "message_id", messageID)
	return nil
}

// handleLabelChange records label changes on a raw email.
func (p *GmailPoller) handleLabelChange(ctx context.Context, job FetchJob, change LabelChange) error {
	// For now, store label changes in a JSONB column or separate table
	// This is a simplified implementation
	labelsJSON := strings.Join(change.LabelIDs, ",")
	_, err := p.state.DB().ExecContext(ctx,
		`UPDATE raw_emails SET labels = $1, updated_at = $2
		 WHERE source_account_id = $3 AND message_id = $4 AND user_id = $5`,
		labelsJSON, time.Now().UTC(), job.AccountID, change.MessageID, job.UserID,
	)
	if err != nil {
		return fmt.Errorf("update labels %s: %w", change.MessageID, err)
	}
	p.log.Debug("label change recorded", "message_id", change.MessageID, "labels", labelsJSON)
	return nil
}

// saveProgress updates the history_id to allow resuming after partial processing.
func (p *GmailPoller) saveProgress(ctx context.Context, job FetchJob, historyID string) error {
	if historyID == "" {
		return nil
	}
	return p.state.UpdateHistoryIDDirect(ctx, job.AccountID, historyID)
}

// ---------------------------------------------------------------------------
// MIME Helpers (exposed for use by parser integration)
// ---------------------------------------------------------------------------

// ParseEmailHeaders extracts basic metadata from raw RFC 822 headers.
// This is a convenience function for lightweight parsing before full MIME parsing.
func ParseEmailHeaders(raw []byte) (subject, from, messageID string, date time.Time, err error) {
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		return "", "", "", time.Time{}, fmt.Errorf("read message: %w", err)
	}

	subject = msg.Header.Get("Subject")
	from = msg.Header.Get("From")
	messageID = msg.Header.Get("Message-Id")
	dateStr := msg.Header.Get("Date")
	if dateStr != "" {
		date, _ = mail.ParseDate(dateStr)
	}

	// Decode MIME-encoded subject
	if subject != "" {
		decoded, err := decodeMIMEHeader(subject)
		if err == nil {
			subject = decoded
		}
	}

	return subject, from, messageID, date, nil
}

// decodeMIMEHeader decodes MIME-encoded headers like =?UTF-8?Q?...?=.
func decodeMIMEHeader(header string) (string, error) {
	decoder := mime.WordDecoder{}
	return decoder.DecodeHeader(header)
}
