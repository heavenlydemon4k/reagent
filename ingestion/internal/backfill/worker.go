package backfill

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/decisionstack/ingestion/internal/models"
	natsevents "github.com/decisionstack/ingestion/internal/nats"
	"github.com/decisionstack/ingestion/internal/poll"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ---------------------------------------------------------------------------
// Worker — processes backfill jobs from the Redis queue
// ---------------------------------------------------------------------------

// Worker consumes backfill jobs and processes historical email data.
// It runs as a separate binary to avoid interfering with real-time ingestion.
type Worker struct {
	db        *sql.DB
	redis     *redis.Client
	scheduler *Scheduler
	gmail     poll.GmailFetcher
	outlook   poll.OutlookFetcher
	tokens    poll.TokenStore
	parser    poll.MIMEParser
	publisher natsevents.Publisher
	log       *slog.Logger
}

// NewWorker creates a new backfill worker.
func NewWorker(
	db *sql.DB,
	redisClient *redis.Client,
	gmail poll.GmailFetcher,
	outlook poll.OutlookFetcher,
	tokens poll.TokenStore,
	parser poll.MIMEParser,
	publisher natsevents.Publisher,
	log *slog.Logger,
) *Worker {
	return &Worker{
		db:        db,
		redis:     redisClient,
		scheduler: NewScheduler(redisClient, log),
		gmail:     gmail,
		outlook:   outlook,
		tokens:    tokens,
		parser:    parser,
		publisher: publisher,
		log:       log.With("component", "backfill_worker"),
	}
}

// Run starts the worker loop. It blocks until the context is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	w.log.Info("backfill worker started")

	for {
		select {
		case <-ctx.Done():
			w.log.Info("backfill worker shutting down")
			return nil
		default:
		}

		// Block until a job is available
		job, err := w.scheduler.Dequeue(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.log.Error("failed to dequeue job", "error", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// Process the job
		if err := w.ProcessJob(ctx, job); err != nil {
			w.log.Error("job processing failed",
				"user_id", job.UserID,
				"account_id", job.AccountID,
				"error", err,
				"retries", job.RetryCount,
			)

			if job.RetryCount < MaxRetries {
				// Calculate backoff: 2^retry * 5 seconds (5s, 10s, 20s)
				backoff := time.Duration(1<<job.RetryCount) * 5 * time.Second
				w.log.Info("retrying after backoff",
					"backoff", backoff,
					"retry", job.RetryCount+1,
				)
				time.Sleep(backoff)

				if rqErr := w.scheduler.RequeueForRetry(ctx, job); rqErr != nil {
					w.log.Error("failed to requeue for retry", "error", rqErr)
					_ = w.scheduler.MarkFailed(ctx, job, fmt.Sprintf("retry requeue failed: %v", rqErr))
				}
			} else {
				_ = w.scheduler.MarkFailed(ctx, job, err.Error())
			}
		}
	}
}

// ProcessJob processes a single backfill job end-to-end.
func (w *Worker) ProcessJob(ctx context.Context, job *BackfillJob) error {
	log := w.log.With(
		"user_id", job.UserID,
		"account_id", job.AccountID,
		"provider", job.Provider,
	)
	log.Info("starting backfill job",
		"start_date", job.StartDate.Format("2006-01-02"),
		"end_date", job.EndDate.Format("2006-01-02"),
	)

	// 1. Load tokens (refresh if needed)
	tokenPair, err := w.tokens.RefreshIfNeeded(ctx, job.AccountID)
	if err != nil {
		return fmt.Errorf("refresh tokens: %w", err)
	}
	accessToken := *tokenPair.AccessTokenPlaintext

	// 2. Route to provider-specific strategy
	switch job.Provider {
	case "gmail":
		if err := w.processGmailBackfill(ctx, job, accessToken, log); err != nil {
			return fmt.Errorf("gmail backfill: %w", err)
		}
	case "outlook":
		if err := w.processOutlookBackfill(ctx, job, accessToken, log); err != nil {
			return fmt.Errorf("outlook backfill: %w", err)
		}
	default:
		return fmt.Errorf("unsupported provider: %s", job.Provider)
	}

	// 3. Mark complete and cleanup
	if err := w.scheduler.MarkComplete(ctx, job); err != nil {
		log.Error("failed to mark job complete", "error", err)
	}

	log.Info("backfill job completed",
		"emails_found", job.EmailsFound,
		"emails_processed", job.EmailsProcessed,
		"emails_skipped", job.EmailsSkipped,
		"emails_failed", job.EmailsFailed,
	)
	return nil
}

// ---------------------------------------------------------------------------
// Gmail backfill strategy
// ---------------------------------------------------------------------------

// processGmailBackfill lists all messages from the last 90 days and processes
// each one through the standard ingestion pipeline.
func (w *Worker) processGmailBackfill(ctx context.Context, job *BackfillJob, accessToken string, log *slog.Logger) error {
	// Build Gmail search query for the date range
	// Gmail search syntax: newer_than:90d or after:YYYY/MM/before:YYYY/MM/DD
	daysBack := int(time.Since(job.StartDate).Hours() / 24)
	query := fmt.Sprintf("newer_than:%dd", daysBack)

	log.Info("listing gmail messages", "query", query)

	// Paginate through all messages
	var allMessages []poll.MessageListItem
	var nextPageToken string
	pageCount := 0

	for {
		pageCount++
		result, err := w.gmail.MessagesList(ctx, accessToken, query, nextPageToken)
		if err != nil {
			return fmt.Errorf("messages.list page %d: %w", pageCount, err)
		}

		allMessages = append(allMessages, result.Messages...)
		nextPageToken = result.NextPageToken

		log.Debug("gmail messages.list page",
			"page", pageCount,
			"messages_this_page", len(result.Messages),
			"total_so_far", len(allMessages),
		)

		if nextPageToken == "" {
			break
		}

		// Check context between pages
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	job.EmailsFound = len(allMessages)
	log.Info("gmail message listing complete", "total_messages", job.EmailsFound)

	// Update initial progress
	if err := w.scheduler.UpdateProgress(ctx, job); err != nil {
		log.Warn("failed to update progress after listing", "error", err)
	}

	// Process in batches of BatchSize
	return w.processGmailMessages(ctx, job, accessToken, allMessages, log)
}

// processGmailMessages fetches, parses, persists, and publishes each Gmail message.
func (w *Worker) processGmailMessages(ctx context.Context, job *BackfillJob, accessToken string, messages []poll.MessageListItem, log *slog.Logger) error {
	for i, msg := range messages {
		// Check rate limit before each email
		allowed, err := w.scheduler.CanProcessEmail(ctx, job.UserID)
		if err != nil {
			log.Error("rate limit check failed", "error", err)
			return fmt.Errorf("rate limit check: %w", err)
		}
		if !allowed {
			log.Warn("rate limit reached, pausing for 1 hour",
				"processed_so_far", job.EmailsProcessed,
			)
			// Sleep for the rate limit window and try again
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(RateLimitWindow):
				// Re-check after window
				allowed, err = w.scheduler.CanProcessEmail(ctx, job.UserID)
				if err != nil || !allowed {
					return fmt.Errorf("rate limit still exceeded after waiting")
				}
			}
		}

		if err := w.processSingleGmailMessage(ctx, job, accessToken, msg); err != nil {
			job.EmailsFailed++
			log.Error("failed to process message",
				"message_id", msg.ID,
				"index", i,
				"error", err,
			)
			// Continue with next message — don't fail the entire job for one bad message
			continue
		}

		job.EmailsProcessed++

		// Update progress every ProgressUpdateInterval emails
		if job.EmailsProcessed%ProgressUpdateInterval == 0 {
			if job.EmailsFound > 0 {
				job.Progress = (job.EmailsProcessed * 100) / job.EmailsFound
			}
			if err := w.scheduler.UpdateProgress(ctx, job); err != nil {
				log.Warn("failed to update progress", "error", err)
			}
			log.Info("backfill progress",
				"processed", job.EmailsProcessed,
				"found", job.EmailsFound,
				"progress_pct", job.Progress,
			)
		}
	}

	// Final progress update
	if job.EmailsFound > 0 {
		job.Progress = (job.EmailsProcessed * 100) / job.EmailsFound
	}
	return nil
}

// processSingleGmailMessage fetches one Gmail message and runs it through
// the standard pipeline: fetch → parse → persist → publish.
func (w *Worker) processSingleGmailMessage(ctx context.Context, job *BackfillJob, accessToken string, msgItem poll.MessageListItem) error {
	// Fetch the full raw message
	msg, err := w.gmail.MessagesGet(ctx, accessToken, msgItem.ID)
	if err != nil {
		return fmt.Errorf("messages.get %s: %w", msgItem.ID, err)
	}
	if msg == nil {
		// Message was deleted between listing and fetching
		job.EmailsSkipped++
		return nil
	}

	// Decode base64url raw content
	rawBytes, err := base64.URLEncoding.DecodeString(msg.Raw)
	if err != nil {
		// Try standard base64 as fallback
		rawBytes, err = base64.StdEncoding.DecodeString(msg.Raw)
		if err != nil {
			return fmt.Errorf("decode raw message %s: %w", msgItem.ID, err)
		}
	}

	// Parse MIME
	parsed, err := w.parser.Parse(rawBytes, job.AccountID, job.UserID)
	if err != nil {
		return fmt.Errorf("parse MIME %s: %w", msgItem.ID, err)
	}

	// Persist + publish (same as real-time pipeline)
	return w.persistAndPublish(ctx, job, parsed, "gmail", msgItem.ID, msgItem.ThreadID)
}

// ---------------------------------------------------------------------------
// Outlook backfill strategy
// ---------------------------------------------------------------------------

// processOutlookBackfill uses Delta Query with an empty deltaLink to perform
// a full sync of the user's mailbox for the last 90 days.
func (w *Worker) processOutlookBackfill(ctx context.Context, job *BackfillJob, accessToken string, log *slog.Logger) error {
	log.Info("starting outlook delta backfill (full sync)")

	var allMessages []poll.OutlookMessage
	deltaLink := "" // Empty = full sync
	pageCount := 0

	for {
		pageCount++

		result, err := w.outlook.DeltaQuery(ctx, accessToken, deltaLink)
		if err != nil {
			return fmt.Errorf("delta query page %d: %w", pageCount, err)
		}

		// Handle rate limiting from API
		if result.RateLimited {
			backoff := result.RetryAfter
			if backoff <= 0 {
				backoff = 60 * time.Second
			}
			log.Warn("outlook API rate limited, backing off", "backoff", backoff)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
				continue // retry the same page
			}
		}

		if result.ErrorCode != "" {
			return fmt.Errorf("outlook delta query error: %s", result.ErrorCode)
		}

		// Collect non-deleted messages within date range
		for _, msg := range result.Messages {
			if msg.ChangeType == "deleted" {
				continue
			}
			// Filter by date range
			if !msg.ReceivedDateTime.IsZero() &&
				(msg.ReceivedDateTime.Before(job.StartDate) || msg.ReceivedDateTime.After(job.EndDate)) {
				continue
			}
			allMessages = append(allMessages, msg)
		}

		log.Debug("outlook delta page",
			"page", pageCount,
			"messages_this_page", len(result.Messages),
			"total_so_far", len(allMessages),
		)

		// Follow pagination via nextLink
		if result.NextLink != "" {
			deltaLink = result.NextLink
			continue
		}

		// We've reached the end (deltaLink is the bookmark for next poll)
		if result.DeltaLink != "" {
			log.Debug("reached end of delta query", "delta_link", truncate(result.DeltaLink, 60))
		}
		break
	}

	job.EmailsFound = len(allMessages)
	log.Info("outlook message listing complete", "total_messages", job.EmailsFound)

	// Update initial progress
	if err := w.scheduler.UpdateProgress(ctx, job); err != nil {
		log.Warn("failed to update progress after listing", "error", err)
	}

	// Process all collected messages
	return w.processOutlookMessages(ctx, job, accessToken, allMessages, log)
}

// processOutlookMessages processes each Outlook message through the standard pipeline.
func (w *Worker) processOutlookMessages(ctx context.Context, job *BackfillJob, accessToken string, messages []poll.OutlookMessage, log *slog.Logger) error {
	for i, msg := range messages {
		// Check context
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check rate limit
		allowed, err := w.scheduler.CanProcessEmail(ctx, job.UserID)
		if err != nil {
			return fmt.Errorf("rate limit check: %w", err)
		}
		if !allowed {
			log.Warn("rate limit reached, pausing for 1 hour",
				"processed_so_far", job.EmailsProcessed,
			)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(RateLimitWindow):
				allowed, err = w.scheduler.CanProcessEmail(ctx, job.UserID)
				if err != nil || !allowed {
					return fmt.Errorf("rate limit still exceeded after waiting")
				}
			}
		}

		// Skip drafts
		if msg.IsDraft {
			job.EmailsSkipped++
			continue
		}

		if err := w.processSingleOutlookMessage(ctx, job, accessToken, msg); err != nil {
			job.EmailsFailed++
			log.Error("failed to process outlook message",
				"message_id", msg.ID,
				"index", i,
				"error", err,
			)
			continue
		}

		job.EmailsProcessed++

		// Update progress
		if job.EmailsProcessed%ProgressUpdateInterval == 0 {
			if job.EmailsFound > 0 {
				job.Progress = (job.EmailsProcessed * 100) / job.EmailsFound
			}
			if err := w.scheduler.UpdateProgress(ctx, job); err != nil {
				log.Warn("failed to update progress", "error", err)
			}
			log.Info("backfill progress",
				"processed", job.EmailsProcessed,
				"found", job.EmailsFound,
				"progress_pct", job.Progress,
			)
		}
	}

	// Final progress
	if job.EmailsFound > 0 {
		job.Progress = (job.EmailsProcessed * 100) / job.EmailsFound
	}
	return nil
}

// processSingleOutlookMessage converts and persists a single Outlook message.
func (w *Worker) processSingleOutlookMessage(ctx context.Context, job *BackfillJob, accessToken string, msg poll.OutlookMessage) error {
	// Convert to ParsedEmail
	parsed := convertOutlookMessageToParsed(msg, job.AccountID, job.UserID)

	// Persist + publish
	return w.persistAndPublish(ctx, job, parsed, "outlook", msg.InternetMessageID, msg.ConversationID)
}

// ---------------------------------------------------------------------------
// Shared: persist + publish (the standard ingestion pipeline)
// ---------------------------------------------------------------------------

// persistAndPublish inserts the parsed email into raw_emails (with ON CONFLICT DO
// NOTHING for deduplication) and publishes the email.ingested event.
// This is the SAME pipeline used by real-time ingestion.
func (w *Worker) persistAndPublish(ctx context.Context, job *BackfillJob, parsed *models.ParsedEmail, source, sourceMessageID, threadID string) error {
	now := time.Now().UTC()
	rawEmailID := uuid.New()

	// Extract S3 URIs from attachments for the attachment_s3_uris column (TEXT[])
	var s3URIs []string
	for _, att := range parsed.Attachments {
		if att.S3URI != "" {
			s3URIs = append(s3URIs, att.S3URI)
		}
	}

	// Insert into raw_emails with ON CONFLICT DO NOTHING.
	// If the email was already processed via webhook, it will be silently skipped.
	res, err := w.db.ExecContext(ctx, `
		INSERT INTO raw_emails (
			id, thread_id, user_id, source_account_id, message_id,
			in_reply_to, references, sender_email, sender_name,
			recipient_emails, subject, body_text, body_html,
			has_attachments, attachment_s3_uris, extracted_codes,
			received_at, parsed_at, retention_until, classification,
			deleted, is_backfill
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, false, true)
		ON CONFLICT (source_account_id, message_id) DO NOTHING
	`,
		rawEmailID,
		threadID,
		job.UserID,
		job.AccountID,
		parsed.MessageID,
		parsed.InReplyTo,
		parsed.References,
		parsed.SenderEmail,
		parsed.SenderName,
		parsed.RecipientEmails,
		parsed.Subject,
		parsed.BodyText,
		parsed.BodyHTML,
		parsed.HasAttachments,
		s3URIs,
		parsed.ExtractedCodes,
		parsed.ReceivedAt,
		now,
		now.Add(30*24*time.Hour), // 30-day retention
		"pending",
	)
	if err != nil {
		return fmt.Errorf("persist email: %w", err)
	}

	// Check if the insert was skipped due to conflict (duplicate)
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		job.EmailsSkipped++
		return nil // Silently skip duplicate
	}

	// Publish email.ingested event (same as real-time)
	event := natsevents.EmailIngestedEvent{
		EventID:            uuid.New(),
		UserID:             job.UserID,
		Source:             source,
		AccountID:          job.AccountID,
		ThreadID:           uuid.Nil, // set by threading engine
		RawEmailID:         rawEmailID,
		S3URI:              parsed.S3URI,
		HasAttachments:     parsed.HasAttachments,
		SenderEmail:        parsed.SenderEmail,
		ReceivedAt:         parsed.ReceivedAt,
		ClassificationHint: "pending",
		ContactIDs:         nil, // set by dedup engine
	}

	if err := w.publisher.PublishEmailIngested(ctx, event); err != nil {
		// Log but don't fail — the email is persisted, event can be replayed
		w.log.Error("failed to publish email.ingested event",
			"raw_email_id", rawEmailID,
			"error", err,
		)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Outlook message conversion (mirrors poll.OutlookPoller.convertToParsedEmail)
// ---------------------------------------------------------------------------

func convertOutlookMessageToParsed(msg poll.OutlookMessage, accountID, userID uuid.UUID) *models.ParsedEmail {
	// Extract sender
	senderEmail := ""
	senderName := ""
	if msg.From.EmailAddress.Address != "" {
		senderEmail = msg.From.EmailAddress.Address
		senderName = msg.From.EmailAddress.Name
	} else if msg.Sender.EmailAddress.Address != "" {
		senderEmail = msg.Sender.EmailAddress.Address
		senderName = msg.Sender.EmailAddress.Name
	}

	// Extract recipients
	var recipients []string
	for _, r := range msg.ToRecipients {
		if r.EmailAddress.Address != "" {
			recipients = append(recipients, r.EmailAddress.Address)
		}
	}
	for _, r := range msg.CcRecipients {
		if r.EmailAddress.Address != "" {
			recipients = append(recipients, r.EmailAddress.Address)
		}
	}

	// Extract body
	bodyText := ""
	bodyHTML := ""
	if msg.Body.ContentType == "text" {
		bodyText = msg.Body.Content
	} else {
		bodyHTML = msg.Body.Content
		if bodyText == "" {
			bodyText = msg.BodyPreview
		}
	}

	// Extract threading headers
	var inReplyTo *string
	var references []string
	for _, h := range msg.InternetMessageHeaders {
		switch h.Name {
		case "In-Reply-To":
			inReplyTo = &h.Value
		case "References":
			references = parseReferences(h.Value)
		}
	}

	// Extract attachments
	var hasAttachments bool
	var attachments []models.Attachment
	for _, att := range msg.Attachments {
		hasAttachments = true
		attachments = append(attachments, models.Attachment{
			Filename:    att.Name,
			ContentType: att.ContentType,
			Size:        att.Size,
			IsInline:    att.IsInline,
		})
	}

	return &models.ParsedEmail{
		ID:              uuid.Nil,
		UserID:          userID,
		AccountID:       accountID,
		Source:          "outlook",
		MessageID:       msg.InternetMessageID,
		InReplyTo:       inReplyTo,
		References:      references,
		SenderEmail:     senderEmail,
		SenderName:      senderName,
		RecipientEmails: recipients,
		Subject:         msg.Subject,
		BodyText:        bodyText,
		BodyHTML:        bodyHTML,
		HasAttachments:  hasAttachments,
		Attachments:     attachments,
		ReceivedAt:      msg.ReceivedDateTime,
	}
}

func parseReferences(refs string) []string {
	var result []string
	for _, r := range strings.Fields(refs) {
		rStr := strings.TrimSpace(r)
		rStr = strings.Trim(rStr, "<>")
		rStr = strings.TrimSpace(rStr)
		if rStr != "" {
			result = append(result, rStr)
		}
	}
	return result
}

// truncate truncates a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
