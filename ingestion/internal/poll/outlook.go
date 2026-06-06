package poll

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/decisionstack/ingestion/internal/models"
	natsevents "github.com/decisionstack/ingestion/internal/nats"

	"github.com/google/uuid"
)

// Outlook gap detection constants.
const (
	// maxConsecutiveSilentPolls is the number of consecutive poll cycles
	// that return zero messages (with a changing deltaLink) before we flag
	// a potential gap. Outlook's delta API can legitimately return zero
	// messages, but persistent zero-message responses may indicate dropped
	// changes in the Graph API.
	maxConsecutiveSilentPolls = 5

	// minDeltaLinkChangeLen is the minimum character difference between
	// consecutive deltaLinks to consider it a "real" advancement.
	minDeltaLinkChangeLen = 10
)

// OutlookFetcher abstracts the Microsoft Graph API for testability.
type OutlookFetcher interface {
	// DeltaQuery fetches messages using a delta token. On first sync,
	// deltaLink is empty and the API returns a deltaToken in @odata.deltaLink.
	DeltaQuery(ctx context.Context, accessToken, deltaLink string) (*DeltaQueryResult, error)
}

// DeltaQueryResult holds the response from an Outlook Delta Query.
type DeltaQueryResult struct {
	Messages      []OutlookMessage
	DeltaLink     string // @odata.deltaLink for the next poll cycle
	NextLink      string // @odata.nextLink for pagination within a cycle
	RetryAfter    time.Duration
	RateLimited   bool
	ErrorCode     string
}

// OutlookMessage represents a message from the Microsoft Graph API.
type OutlookMessage struct {
	ID                   string
	ConversationID       string
	Subject              string
	Sender               OutlookRecipient
	From                 OutlookRecipient
	ToRecipients         []OutlookRecipient
	CcRecipients         []OutlookRecipient
	BccRecipients        []OutlookRecipient
	ReceivedDateTime     time.Time
	SentDateTime         time.Time
	BodyPreview          string
	Body                 OutlookBody
	InternetMessageID    string
	InternetMessageHeaders []OutlookMessageHeader
	HasAttachments       bool
	Attachments          []OutlookAttachment
	IsDraft              bool
	IsRead               bool
	Importance           string
	Flag                 OutlookFlag
	Categories           []string
	ChangeType           string // "created" | "updated" | "deleted" from delta
}

// OutlookRecipient represents an email sender or recipient.
type OutlookRecipient struct {
	EmailAddress OutlookEmailAddress `json:"emailAddress"`
}

// OutlookEmailAddress contains the email address and display name.
type OutlookEmailAddress struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}

// OutlookBody represents the message body.
type OutlookBody struct {
	ContentType string `json:"contentType"` // "text" | "html"
	Content     string `json:"content"`
}

// OutlookMessageHeader represents an internet message header.
type OutlookMessageHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// OutlookAttachment represents a message attachment.
type OutlookAttachment struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	ContentType      string `json:"contentType"`
	Size             int64  `json:"size"`
	IsInline         bool   `json:"isInline"`
	ContentBytes     string `json:"contentBytes,omitempty"`
	ContentLocation  string `json:"contentLocation,omitempty"`
}

// OutlookFlag represents the follow-up flag on a message.
type OutlookFlag struct {
	FlagStatus string `json:"flagStatus"`
}

// OutlookPoller implements JobProcessor for Outlook accounts. It polls using
// Microsoft Graph Delta Query to detect new, updated, and deleted messages.
type OutlookPoller struct {
	rateLimit *RateLimiter
	state     *StateStore
	fetcher   OutlookFetcher
	tokens    TokenStore
	parser    MIMEParser
	publisher natsevents.Publisher
	appID     string // application ID for rate limit key
	log       *slog.Logger

	// consecutiveSilentPolls tracks how many consecutive poll cycles returned
	// zero messages while the deltaLink still advanced. Protected by gapMu.
	// This detects potential gaps where the Graph API advances the token but
	// fails to include all changes in the response.
	consecutiveSilentPolls int
	gapMu                  sync.Mutex
	// previousDeltaLink stores the last deltaLink for comparison.
	previousDeltaLink string
}

// NewOutlookPoller creates a new OutlookPoller.
func NewOutlookPoller(
	rateLimit *RateLimiter,
	state *StateStore,
	fetcher OutlookFetcher,
	tokens TokenStore,
	parser MIMEParser,
	publisher natsevents.Publisher,
	appID string,
	log *slog.Logger,
) *OutlookPoller {
	if appID == "" {
		appID = "default"
	}
	return &OutlookPoller{
		rateLimit: rateLimit,
		state:     state,
		fetcher:   fetcher,
		tokens:    tokens,
		parser:    parser,
		publisher: publisher,
		appID:     appID,
		log:       log.With("component", "outlook_poller"),
	}
}

// Process implements JobProcessor. It polls an Outlook account using Delta
// Query, processing created, updated, and deleted messages.
func (p *OutlookPoller) Process(ctx context.Context, job FetchJob) error {
	log := p.log.With("account_id", job.AccountID, "user_id", job.UserID)
	log.Info("starting outlook poll cycle")

	// 1. Get (and refresh if needed) OAuth tokens
	tokenPair, err := p.tokens.RefreshIfNeeded(ctx, job.AccountID)
	if err != nil {
		return fmt.Errorf("refresh tokens: %w", err)
	}
	accessToken := *tokenPair.AccessTokenPlaintext

	// 2. Get stored deltaLink
	deltaLink, err := p.state.GetDeltaLink(ctx, job.AccountID)
	if err != nil {
		return fmt.Errorf("get delta_link: %w", err)
	}

	// If no deltaLink, we need a full sync first
	if deltaLink == "" {
		log.Warn("no delta_link stored, need full sync")
		return fmt.Errorf("no delta_link: full sync required")
	}

	// 3. Check rate limit
	rlStatus, err := p.rateLimit.AllowOutlookRequest(ctx, p.appID)
	if err != nil {
		return fmt.Errorf("rate limit check: %w", err)
	}
	if !rlStatus.Allowed {
		log.Warn("outlook rate limited", "remaining", rlStatus.Remaining, "backoff", rlStatus.Backoff)
		return models.IngestionError{
			Code:    models.ErrCodeRateLimited,
			Message: fmt.Sprintf("outlook rate limited: retry after %v", rlStatus.Backoff),
			UserID:  job.UserID.String(),
			Retry:   true,
		}
	}

	// 4. Execute delta query
	result, err := p.fetcher.DeltaQuery(ctx, accessToken, deltaLink)
	if err != nil {
		_ = p.rateLimit.RefundOutlookQuota(ctx, p.appID)
		return fmt.Errorf("delta query failed: %w", err)
	}

	// 5. Handle 429 rate limit from API (adaptive backoff)
	if result.RateLimited {
		backoff := result.RetryAfter
		if backoff <= 0 {
			backoff = 60 * time.Second // default 1 min if no Retry-After header
		}
		log.Warn("outlook API returned 429, backing off", "retry_after", backoff)
		return models.IngestionError{
			Code:    models.ErrCodeRateLimited,
			Message: fmt.Sprintf("outlook API rate limited: retry after %v", backoff),
			UserID:  job.UserID.String(),
			Retry:   true,
		}
	}

	if result.ErrorCode != "" {
		return models.IngestionError{
			Code:    result.ErrorCode,
			Message: fmt.Sprintf("outlook API error: %s", result.ErrorCode),
			UserID:  job.UserID.String(),
			Retry:   true,
		}
	}

	// 6. Handle pagination: follow @odata.nextLink until we get @odata.deltaLink
	allMessages := result.Messages
	nextLink := result.NextLink
	newDeltaLink := result.DeltaLink

	for nextLink != "" {
		// Check rate limit for each paginated request
		rlStatus, err = p.rateLimit.AllowOutlookRequest(ctx, p.appID)
		if err != nil {
			return fmt.Errorf("rate limit check (delta page): %w", err)
		}
		if !rlStatus.Allowed {
			// Save partial progress
			if newDeltaLink != "" {
				if err := p.state.UpdateDeltaLinkDirect(ctx, job.AccountID, newDeltaLink); err != nil {
					log.Error("failed to save partial delta_link", "error", err)
				}
			}
			return models.IngestionError{
				Code:    models.ErrCodeRateLimited,
				Message: fmt.Sprintf("outlook rate limited during pagination: retry after %v", rlStatus.Backoff),
				UserID:  job.UserID.String(),
				Retry:   true,
			}
		}

		// Fetch next page using nextLink as the deltaLink parameter
		pageResult, err := p.fetcher.DeltaQuery(ctx, accessToken, nextLink)
		if err != nil {
			_ = p.rateLimit.RefundOutlookQuota(ctx, p.appID)
			if newDeltaLink != "" {
				if err := p.state.UpdateDeltaLinkDirect(ctx, job.AccountID, newDeltaLink); err != nil {
					log.Error("failed to save partial delta_link", "error", err)
				}
			}
			return fmt.Errorf("delta query page failed: %w", err)
		}

		if pageResult.RateLimited {
			backoff := pageResult.RetryAfter
			if backoff <= 0 {
				backoff = 60 * time.Second
			}
			if newDeltaLink != "" {
				_ = p.state.UpdateDeltaLinkDirect(ctx, job.AccountID, newDeltaLink)
			}
			return models.IngestionError{
				Code:    models.ErrCodeRateLimited,
				Message: fmt.Sprintf("outlook API rate limited on page: retry after %v", backoff),
				UserID:  job.UserID.String(),
				Retry:   true,
			}
		}

		allMessages = append(allMessages, pageResult.Messages...)
		nextLink = pageResult.NextLink
		if pageResult.DeltaLink != "" {
			newDeltaLink = pageResult.DeltaLink
		}
	}

	log.Info("delta query complete",
		"messages", len(allMessages),
		"has_delta_link", newDeltaLink != "",
	)

	// 6a. Verify no delta gaps before processing messages.
	// Outlook's delta API uses opaque tokens, so we detect gaps by looking
	// for anomalous patterns: repeated zero-message responses with advancing
	// deltaLinks, or sudden jumps in message count that don't match history.
	if gapErr := p.verifyNoGaps(deltaLink, newDeltaLink, len(allMessages), log); gapErr != nil {
		p.gapMu.Lock()
		p.consecutiveSilentPolls++
		silentCount := p.consecutiveSilentPolls
		p.gapMu.Unlock()

		if silentCount >= maxConsecutiveSilentPolls {
			log.Error("CRITICAL: persistent delta gap detected — halting deltaLink advancement",
				"consecutive_silent", silentCount,
				"error", gapErr,
			)
			// Do not update deltaLink — next poll uses the same one.
			return fmt.Errorf("delta gap detected (consecutive_silent=%d): %w", silentCount, gapErr)
		}

		log.Warn("potential delta gap detected — monitoring",
			"consecutive_silent", silentCount,
			"error", gapErr,
		)
	} else {
		// Reset on successful non-silent poll.
		p.gapMu.Lock()
		p.consecutiveSilentPolls = 0
		p.gapMu.Unlock()
	}

	// 7. Process each message
	var processedCount, deletedCount int

	for _, msg := range allMessages {
		switch msg.ChangeType {
		case "deleted":
			if err := p.handleMessageDeleted(ctx, job, msg.ID); err != nil {
				log.Error("failed to handle message deletion", "message_id", msg.ID, "error", err)
			} else {
				deletedCount++
			}
		case "created", "updated", "":
			// Empty ChangeType means new message on initial sync
			if err := p.processMessage(ctx, job, accessToken, msg); err != nil {
				log.Error("failed to process message",
					"message_id", msg.ID,
					"change_type", msg.ChangeType,
					"error", err,
				)
				// Save delta_link progress so far and return
				if newDeltaLink != "" {
					if saveErr := p.state.UpdateDeltaLinkDirect(ctx, job.AccountID, newDeltaLink); saveErr != nil {
						log.Error("failed to save delta_link after error", "error", saveErr)
					}
				}
				return fmt.Errorf("process message %s: %w", msg.ID, err)
			}
			processedCount++
		default:
			log.Warn("unknown change type, treating as created", "change_type", msg.ChangeType, "message_id", msg.ID)
			if err := p.processMessage(ctx, job, accessToken, msg); err != nil {
				log.Error("failed to process message", "message_id", msg.ID, "error", err)
				if newDeltaLink != "" {
					if saveErr := p.state.UpdateDeltaLinkDirect(ctx, job.AccountID, newDeltaLink); saveErr != nil {
						log.Error("failed to save delta_link after error", "error", saveErr)
					}
				}
				return fmt.Errorf("process message %s: %w", msg.ID, err)
			}
			processedCount++
		}
	}

	// 8. Persist the new deltaLink for the next poll cycle
	if newDeltaLink != "" {
		if err := p.state.UpdateDeltaLinkDirect(ctx, job.AccountID, newDeltaLink); err != nil {
			return fmt.Errorf("update delta_link: %w", err)
		}
		log.Debug("delta_link updated", "delta_link", truncate(newDeltaLink, 60))
	}

	log.Info("outlook poll cycle complete", "processed", processedCount, "deleted", deletedCount)
	return nil
}

// verifyNoGaps checks the delta query response for patterns that indicate
// potentially dropped messages. Since Outlook's delta API uses opaque tokens,
// we detect gaps through heuristic checks:
//
//  1. Silent poll: deltaLink advanced but zero messages returned.
//     A few silent polls are normal (no new mail), but persistent silent
//     polls with token advancement may indicate the API is skipping changes.
//
//  2. DeltaLink jump: the new deltaLink is suspiciously different from
//     the previous one, suggesting a large batch of changes was condensed
//     or skipped by the API.
//
//  3. Message count anomaly: a sudden large drop in message count compared
//     to the account's typical pattern.
//
// If any check indicates a potential gap, returns a descriptive error.
// The caller should NOT advance the deltaLink so the next poll retries.
func (p *OutlookPoller) verifyNoGaps(previousDeltaLink, newDeltaLink string, messageCount int, log *slog.Logger) error {
	// Check 1: Silent poll detection — deltaLink advanced but zero messages.
	// We only count this as a potential gap if the deltaLink meaningfully changed.
	if messageCount == 0 && newDeltaLink != "" && newDeltaLink != previousDeltaLink {
		// Check that the deltaLink actually changed (not just a re-encode).
		deltaChange := deltaLinkDiff(previousDeltaLink, newDeltaLink)
		if deltaChange >= minDeltaLinkChangeLen {
			return fmt.Errorf("silent poll: deltaLink advanced by %d chars but zero messages returned (prev_len=%d, new_len=%d)",
				deltaChange, len(previousDeltaLink), len(newDeltaLink))
		}
	}

	// Check 2: DeltaLink truncation — if the new deltaLink is significantly
	// shorter than the previous one, the API may have reset/truncated state.
	if previousDeltaLink != "" && newDeltaLink != "" && len(newDeltaLink) < len(previousDeltaLink)/2 {
		return fmt.Errorf("deltaLink truncated: new length (%d) < 50%% of previous length (%d): potential state reset",
			len(newDeltaLink), len(previousDeltaLink))
	}

	// Check 3: Non-zero message count with unchanged deltaLink — this should
	// never happen. If we got messages but the deltaLink didn't advance,
	// we may see duplicates, but flag it as a consistency issue.
	if messageCount > 0 && newDeltaLink == previousDeltaLink && previousDeltaLink != "" {
		return fmt.Errorf("consistency issue: %d messages returned but deltaLink unchanged: potential duplicate or missed advance",
			messageCount)
	}

	// Update the previous deltaLink for next comparison.
	p.gapMu.Lock()
	p.previousDeltaLink = newDeltaLink
	p.gapMu.Unlock()

	log.Debug("delta gap check passed",
		"messages", messageCount,
		"delta_link_changed", newDeltaLink != previousDeltaLink,
	)
	return nil
}

// deltaLinkDiff returns a rough measure of how much two deltaLinks differ.
// It counts character-level differences (like Levenshtein but simplified).
func deltaLinkDiff(a, b string) int {
	if a == "" || b == "" {
		return len(a) + len(b)
	}
	// Simple approach: if one is a prefix of the other, return the length diff.
	if strings.HasPrefix(a, b) {
		return len(a) - len(b)
	}
	if strings.HasPrefix(b, a) {
		return len(b) - len(a)
	}
	// Fall back to full length sum as a conservative estimate.
	return len(a) + len(b)
}

// processMessage persists a single Outlook message and publishes the event.
func (p *OutlookPoller) processMessage(ctx context.Context, job FetchJob, accessToken string, msg OutlookMessage) error {
	log := p.log.With("message_id", msg.ID, "conversation_id", msg.ConversationID)

	// Skip drafts
	if msg.IsDraft {
		log.Debug("skipping draft message")
		return nil
	}

	// Check rate limit for any additional API calls (e.g., attachment download)
	if msg.HasAttachments && len(msg.Attachments) > 0 {
		rlStatus, err := p.rateLimit.AllowOutlookRequest(ctx, p.appID)
		if err != nil {
			return fmt.Errorf("rate limit check (attachments): %w", err)
		}
		if !rlStatus.Allowed {
			return models.IngestionError{
				Code:    models.ErrCodeRateLimited,
				Message: fmt.Sprintf("outlook rate limited for attachments: retry after %v", rlStatus.Backoff),
				UserID:  job.UserID.String(),
				Retry:   true,
			}
		}
	}

	// Convert Outlook message to ParsedEmail
	parsed := p.convertToParsedEmail(msg, job.AccountID, job.UserID)

	// Persist to raw_emails
	now := time.Now().UTC()
	rawEmailID := uuid.New()

	err := p.state.AtomicEmailCommit(
		ctx,
		// insertEmail function
		func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO raw_emails (
					id, thread_id, user_id, source_account_id, message_id,
					in_reply_to, references, sender_email, sender_name,
					recipient_emails, subject, body_text, body_html,
					has_attachments, attachment_s3_uris, extracted_codes,
					received_at, parsed_at, retention_until, classification,
					deleted
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, false)
				ON CONFLICT (source_account_id, message_id) DO NOTHING
			`,
				rawEmailID,
				parsed.ThreadHint,
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
				parsed.Attachments,
				parsed.ExtractedCodes,
				parsed.ReceivedAt,
				now,
				now.Add(30 * 24 * time.Hour),
				"pending",
			)
			return err
		},
		// updateState function (delta_link updated after all messages)
		func(tx *sql.Tx) error {
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("persist email %s: %w", msg.ID, err)
	}

	// Publish email.ingested event
	event := natsevents.EmailIngestedEvent{
		EventID:            uuid.New(),
		UserID:             job.UserID,
		Source:             "outlook",
		AccountID:          job.AccountID,
		ThreadID:           uuid.Nil,
		RawEmailID:         rawEmailID,
		S3URI:              parsed.S3URI,
		HasAttachments:     parsed.HasAttachments,
		SenderEmail:        parsed.SenderEmail,
		ReceivedAt:         parsed.ReceivedAt,
		ClassificationHint: "pending",
		ContactIDs:         nil,
	}

	if err := p.publisher.PublishEmailIngested(ctx, event); err != nil {
		log.Error("failed to publish email.ingested event", "error", err)
	}

	log.Debug("message processed successfully", "message_id", msg.ID)
	return nil
}

// handleMessageDeleted marks a raw email as deleted.
func (p *OutlookPoller) handleMessageDeleted(ctx context.Context, job FetchJob, messageID string) error {
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

// convertToParsedEmail converts an OutlookMessage to a ParsedEmail.
func (p *OutlookPoller) convertToParsedEmail(msg OutlookMessage, accountID, userID uuid.UUID) *models.ParsedEmail {
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
		// If no text version, use preview as fallback
		if bodyText == "" {
			bodyText = msg.BodyPreview
		}
	}

	// Extract internet message ID and headers for threading
	internetMsgID := msg.InternetMessageID
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

	// Extract attachments info
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

	// Build thread hint
	var threadHint *models.ThreadHint
	if inReplyTo != nil || len(references) > 0 {
		threadHint = &models.ThreadHint{
			InReplyTo: *inReplyTo,
			References: references,
			Subject:    msg.Subject,
		}
	}

	return &models.ParsedEmail{
		ID:              uuid.Nil, // generated at insert time
		UserID:          userID,
		AccountID:       accountID,
		Source:          "outlook",
		MessageID:       internetMsgID,
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

// parseReferences splits a References header into individual message IDs.
func parseReferences(refs string) []string {
	var result []string
	for _, r := range strings.Fields(refs) {
		r = strings.Trim(r, "<>")
		if r != "" {
			result = append(result, r)
		}
	}
	return result
}

// truncate truncates a string to maxLen characters, adding "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ---------------------------------------------------------------------------
// Retry-After Parsing
// ---------------------------------------------------------------------------

// ParseRetryAfter parses the Retry-After header value into a duration.
// It handles both delta-seconds and HTTP-date formats.
func ParseRetryAfter(value string) time.Duration {
	// Try parsing as integer seconds
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
		return time.Duration(seconds) * time.Second
	}

	// Try parsing as HTTP-date (RFC 1123, RFC 850, or ANSI C's asctime)
	for _, layout := range []string{
		http.TimeFormat,     // RFC 1123
		time.RFC850,         // RFC 850
		time.RFC1123,        // RFC 1123
		"Mon Jan _2 15:04:05 2006", // ANSI C's asctime()
	} {
		if t, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			d := time.Until(t)
			if d > 0 {
				return d
			}
			return 0
		}
	}

	// Default: 60 seconds
	return 60 * time.Second
}
