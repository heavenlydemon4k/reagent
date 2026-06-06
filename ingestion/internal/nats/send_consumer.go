// Package nats provides the email.send JetStream consumer for the Ingestion Mesh.
// It receives approved drafts from NATS, dispatches them via Gmail/Outlook API,
// and publishes email.sent confirmations.
package nats

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/decisionstack/ingestion/internal/logger"
	"github.com/decisionstack/ingestion/internal/models"
	"github.com/decisionstack/ingestion/internal/oauth"
	"github.com/google/uuid"
	natsgo "github.com/nats-io/nats.go"
)

// NATSSubjectEmailSend is the NATS subject for email send jobs.
const NATSSubjectEmailSend = SubjectEmailSend

// SendJobPayload mirrors sync/internal/decision/approval.go — the wire format
// published to "email.send" when a user approves a draft.
type SendJobPayload struct {
	DraftID    uuid.UUID `json:"draft_id"`
	UserID     uuid.UUID `json:"user_id"`
	ThreadID   uuid.UUID `json:"thread_id"`
	DraftBody  string    `json:"draft_body"`
	Subject    string    `json:"subject"`
	InReplyTo  *string   `json:"in_reply_to,omitempty"`
	References []string  `json:"references,omitempty"`
}

// EmailSentEvent is published to "email.sent" after a draft is successfully
// dispatched via the provider API.
type EmailSentEvent struct {
	DraftID   uuid.UUID `json:"draft_id"`
	MessageID string    `json:"message_id"`
	SentAt    time.Time `json:"sent_at"`
}

// SendConsumer listens for email.send events and dispatches to Gmail/Outlook.
type SendConsumer struct {
	tokenStore *oauth.TokenStore
	google     models.OAuthProvider
	outlook    models.OAuthProvider
	db         *sql.DB
	js         natsgo.JetStreamContext
	log        *logger.Logger
}

// NewSendConsumer creates a send consumer with all required dependencies.
func NewSendConsumer(
	tokenStore *oauth.TokenStore,
	google models.OAuthProvider,
	outlook models.OAuthProvider,
	db *sql.DB,
	js natsgo.JetStreamContext,
	log *logger.Logger,
) *SendConsumer {
	return &SendConsumer{
		tokenStore: tokenStore,
		google:     google,
		outlook:    outlook,
		db:         db,
		js:         js,
		log:        log.With("component", "send-consumer"),
	}
}

// HandleSendMessage processes a single email.send NATS message.
//
// Steps:
//  1. Unmarshal SendJobPayload
//  2. Resolve source email account (draft → decision_card → email_accounts)
//  3. Resolve recipient (original sender of the email being replied to)
//  4. Refresh OAuth tokens if expired via TokenStore
//  5. Build models.SendEmailRequest with threading headers
//  6. Dispatch to the correct provider (Gmail or Outlook)
//  7. Publish email.sent confirmation event
//  8. Ack the NATS message
//
// Retry policy: 3 attempts with exponential backoff (1s, 2s, 4s).
// Non-retryable errors (bad payload, missing account, unsupported provider)
// are acked immediately to prevent redelivery. After all retries are
// exhausted the message is NAK'd for NATS redelivery.
func (c *SendConsumer) HandleSendMessage(ctx context.Context, msg *natsgo.Msg) error {
	// 1. Unmarshal payload
	var payload SendJobPayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		// Non-retryable — ack to avoid redelivery of garbage
		msg.Ack()
		return fmt.Errorf("unmarshal send job payload: %w", err)
	}

	log := c.log.With(
		"draft_id", payload.DraftID,
		"user_id", payload.UserID,
		"thread_id", payload.ThreadID,
	)

	// -------------------------------------------------------------------------
	// Retry loop: resolve, send, confirm
	// -------------------------------------------------------------------------
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			backoff := time.Second * time.Duration(1<<uint(attempt-1))
			log.Warn(ctx, "send attempt failed, retrying",
				"attempt", attempt+1,
				"backoff", backoff,
				"error", lastErr,
			)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during retry backoff: %w", ctx.Err())
			}
		}

		lastErr = c.trySend(ctx, log, payload)
		if lastErr == nil {
			log.Info(ctx, "email sent successfully", "attempt", attempt+1)
			return msg.Ack()
		}

		// Non-retryable errors — stop immediately and ack to drop
		if isNonRetryableSendError(lastErr) {
			log.Error(ctx, "non-retryable send error", "error", lastErr)
			msg.Ack()
			return lastErr
		}
	}

	// All retries exhausted — NAK for redelivery
	log.Error(ctx, "send failed after all retries", "error", lastErr)
	msg.Nak()
	return fmt.Errorf("send failed after 3 attempts: %w", lastErr)
}

// trySend performs a single end-to-end send attempt.
func (c *SendConsumer) trySend(ctx context.Context, log *logger.Logger, payload SendJobPayload) error {
	// 2. Resolve source account via draft → decision_card → email_accounts
	var accountID uuid.UUID
	var providerName string
	var accountEmail string

	err := c.db.QueryRowContext(ctx, `
		SELECT ea.id, ea.provider, ea.email_address
		FROM drafts d
		JOIN decision_cards c ON d.card_id = c.id
		JOIN email_accounts ea ON c.source_account_id = ea.id
		WHERE d.id = $1 AND d.user_id = $2 AND ea.is_active = true
	`, payload.DraftID, payload.UserID).Scan(&accountID, &providerName, &accountEmail)
	if err == sql.ErrNoRows {
		// Fallback: use the user's first active email account
		err = c.db.QueryRowContext(ctx, `
			SELECT id, provider, email_address
			FROM email_accounts
			WHERE user_id = $1 AND is_active = true
			ORDER BY created_at ASC
			LIMIT 1
		`, payload.UserID).Scan(&accountID, &providerName, &accountEmail)
		if err == sql.ErrNoRows {
			return fmt.Errorf("no active email account found for user %s", payload.UserID)
		}
		if err != nil {
			return fmt.Errorf("fallback account lookup failed: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("resolve source account: %w", err)
	}

	log = log.With("account_id", accountID, "provider", providerName)

	// 3. Resolve recipient (reply-to address)
	recipient, err := c.resolveRecipient(ctx, payload)
	if err != nil {
		log.Warn(ctx, "recipient resolution failed", "error", err)
		// Continue with empty To — provider will fail gracefully
	}

	// 4. Refresh tokens if needed (handles decrypt, refresh, encrypt, persist)
	pair, err := c.tokenStore.RefreshIfNeeded(ctx, accountID)
	if err != nil {
		return fmt.Errorf("refresh tokens for account %s: %w", accountID, err)
	}
	if pair.AccessTokenPlaintext == nil {
		return fmt.Errorf("no access token available for account %s", accountID)
	}
	accessToken := *pair.AccessTokenPlaintext

	// 5. Build SendEmailRequest with threading headers
	req := models.SendEmailRequest{
		To:         recipient,
		Subject:    payload.Subject,
		BodyText:   payload.DraftBody,
		InReplyTo:  payload.InReplyTo,
		References: payload.References,
	}

	// 6. Dispatch to the correct provider
	var messageID string
	var sendErr error
	switch providerName {
	case string(oauth.ProviderGmail):
		messageID, sendErr = c.google.SendEmail(ctx, accessToken, req)
	case string(oauth.ProviderOutlook):
		messageID, sendErr = c.outlook.SendEmail(ctx, accessToken, req)
	default:
		return fmt.Errorf("unsupported provider %q for account %s", providerName, accountID)
	}
	if sendErr != nil {
		return fmt.Errorf("provider send failed: %w", sendErr)
	}

	// 7. Publish email.sent confirmation with the real message ID from the provider
	confirm := map[string]interface{}{
		"type":       "email.sent",
		"draft_id":   payload.DraftID,
		"user_id":    payload.UserID,
		"thread_id":  payload.ThreadID,
		"message_id": messageID,
		"sent_at":    time.Now().UTC().Format(time.RFC3339),
	}
	confirmBytes, _ := json.Marshal(confirm)
	if pubErr := c.js.Publish(SubjectEmailSent, confirmBytes); pubErr != nil {
		log.Warn(ctx, "failed to publish email.sent confirmation", "error", pubErr)
		// Non-fatal: email was sent, just confirmation lost
	}

	return nil
}

// resolveRecipient finds the recipient email for a draft reply.
// Strategy:
//   1. Look up raw_emails by thread_id, find the email that is NOT from the user's account
//   2. If draft has explicit To field (future), use that
//   3. Fallback: look up thread's sender_email from raw_emails
func (c *SendConsumer) resolveRecipient(ctx context.Context, draft SendJobPayload) (string, error) {
	// Query: SELECT sender_email FROM raw_emails
	//        WHERE thread_id = (SELECT thread_id FROM decision_cards WHERE id = ...)
	//        AND source_account_id != $user_account
	//        ORDER BY received_at DESC LIMIT 1

	var recipient string
	err := c.db.QueryRowContext(ctx, `
		SELECT re.sender_email
		FROM raw_emails re
		JOIN decision_cards dc ON dc.thread_id = re.thread_id
		WHERE dc.id = $1
		  AND re.source_account_id != (
			  SELECT source_account_id FROM decision_cards WHERE id = $1
		  )
		ORDER BY re.received_at DESC
		LIMIT 1
	`, draft.ThreadID).Scan(&recipient)

	if err == sql.ErrNoRows {
		// Fallback: use the thread's original sender
		err = c.db.QueryRowContext(ctx, `
			SELECT sender_email FROM raw_emails
			WHERE thread_id = $1
			ORDER BY received_at ASC LIMIT 1
		`, draft.ThreadID).Scan(&recipient)
	}

	if err != nil {
		return "", fmt.Errorf("resolve recipient for thread %s: %w", draft.ThreadID, err)
	}
	return recipient, nil
}

// isNonRetryableSendError returns true for errors that will not be fixed by
// retrying (bad payload, missing account, unsupported provider, etc.).
func isNonRetryableSendError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "no active email account found"):
		return true
	case strings.Contains(s, "unsupported provider"):
		return true
	case strings.Contains(s, "unmarshal send job payload"):
		return true
	}
	// Terminal OAuth errors
	if strings.Contains(s, "invalid_grant") {
		return true
	}
	return false
}

// Subscribe starts a pull subscription loop on the "email.send" subject.
// Blocks until the context is cancelled.
func (c *SendConsumer) Subscribe(ctx context.Context) error {
	sub, err := c.js.PullSubscribe(SubjectEmailSend, "send-consumer")
	if err != nil {
		return fmt.Errorf("pull subscribe to %s: %w", SubjectEmailSend, err)
	}
	defer sub.Unsubscribe()

	c.log.Info(ctx, "send consumer subscribed",
		"subject", SubjectEmailSend,
		"consumer", "send-consumer",
	)

	for {
		select {
		case <-ctx.Done():
			c.log.Info(ctx, "send consumer shutting down")
			return nil
		default:
		}

		msgs, err := sub.Fetch(10, natsgo.Context(ctx))
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if err == natsgo.ErrTimeout {
				continue
			}
			c.log.Error(ctx, "fetch error", "error", err)
			time.Sleep(1 * time.Second)
			continue
		}

		for _, msg := range msgs {
			if err := c.HandleSendMessage(ctx, msg); err != nil {
				c.log.Error(ctx, "handle send message failed", "error", err)
			}
		}
	}
}

// ProviderNameFromAccount maps an account identifier (provider name, email
// address, or shorthand) to a canonical provider name ("gmail" or "outlook").
// It is a best-effort heuristic used when the exact provider field is not
// available.
func ProviderNameFromAccount(accountID string) string {
	s := strings.ToLower(accountID)
	switch {
	case strings.Contains(s, "outlook") || strings.Contains(s, "hotmail") || strings.Contains(s, "live") || strings.Contains(s, "microsoft"):
		return string(oauth.ProviderOutlook)
	case strings.Contains(s, "gmail") || strings.Contains(s, "google"):
		return string(oauth.ProviderGmail)
	default:
		// Default to gmail as the most common case
		return string(oauth.ProviderGmail)
	}
}
