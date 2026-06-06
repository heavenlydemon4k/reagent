// Package models defines the shared data structures for the Ingestion Mesh.
// These structs are the contracts between all components and MUST NOT CHANGE
// without coordination across all agent tracks.
package models

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ============================================================================
// RAW EMAIL — Output of Parser, Input to Threading + Dedup + Event Publisher
// ============================================================================

type RawEmail struct {
	ID               uuid.UUID       `db:"id" json:"id"`
	ThreadID         uuid.UUID       `db:"thread_id" json:"thread_id"`
	UserID           uuid.UUID       `db:"user_id" json:"user_id"`
	SourceAccountID  uuid.UUID       `db:"source_account_id" json:"source_account_id"`
	MessageID        string          `db:"message_id" json:"message_id"`
	InReplyTo        *string         `db:"in_reply_to" json:"in_reply_to,omitempty"`
	References       []string        `db:"references" json:"references"`
	SenderEmail      string          `db:"sender_email" json:"sender_email"`
	SenderName       *string         `db:"sender_name" json:"sender_name,omitempty"`
	RecipientEmails  []string        `db:"recipient_emails" json:"recipient_emails"`
	Subject          *string         `db:"subject" json:"subject,omitempty"`
	BodyText         *string         `db:"body_text" json:"body_text,omitempty"`
	BodyHTML         *string         `db:"body_html" json:"body_html,omitempty"`
	HasAttachments   bool            `db:"has_attachments" json:"has_attachments"`
	AttachmentS3URIs []string        `db:"attachment_s3_uris" json:"attachment_s3_uris"`
	ExtractedCodes   []string        `db:"extracted_codes" json:"extracted_codes"`
	ReceivedAt       time.Time       `db:"received_at" json:"received_at"`
	ParsedAt         time.Time       `db:"parsed_at" json:"parsed_at"`
	RetentionUntil   time.Time       `db:"retention_until" json:"retention_until"`
	Classification   *string         `db:"classification" json:"classification,omitempty"`
}

// ParsedEmail is the intermediate representation after parsing but before
// threading and dedup. It is what the Parser Track produces and the
// Threading+Dedup+Event Track consumes.
type ParsedEmail struct {
	ID              uuid.UUID       `json:"id"`
	UserID          uuid.UUID       `json:"user_id"`
	AccountID       uuid.UUID       `json:"account_id"`
	Source          string          `json:"source"` // "gmail" | "outlook"
	MessageID       string          `json:"message_id"`
	InReplyTo       *string         `json:"in_reply_to,omitempty"`
	References      []string        `json:"references"`
	SenderEmail     string          `json:"sender_email"`
	SenderName      string          `json:"sender_name"`
	RecipientEmails []string        `json:"recipient_emails"`
	Subject         string          `json:"subject"`
	BodyText        string          `json:"body_text"`
	BodyHTML        string          `json:"body_html"`
	HasAttachments  bool            `json:"has_attachments"`
	Attachments     []Attachment    `json:"attachments"`
	ExtractedCodes  []string        `json:"extracted_codes"`
	ReceivedAt      time.Time       `json:"received_at"`
	S3URI           string          `json:"s3_uri"` // path to raw blob in S3
	ThreadHint      *ThreadHint     `json:"thread_hint,omitempty"`
}

type Attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	S3URI       string `json:"s3_uri"`
	IsInline    bool   `json:"is_inline"`
}

type ThreadHint struct {
	InReplyTo string   `json:"in_reply_to"`
	References []string `json:"references"`
	Subject    string   `json:"subject"`
}

// ============================================================================
// THREAD — Output of Threading Engine, Input to Event Publisher
// ============================================================================

type Thread struct {
	ID               uuid.UUID `db:"id" json:"id"`
	UserID           uuid.UUID `db:"user_id" json:"user_id"`
	ThreadKey        string    `db:"thread_key" json:"thread_key"` // SHA-256 of sorted participants + subject
	SourceAccountID  uuid.UUID `db:"source_account_id" json:"source_account_id"`
	Subject          *string   `db:"subject" json:"subject,omitempty"`
	ParticipantEmails []string `db:"participant_emails" json:"participant_emails"`
	MessageCount     int       `db:"message_count" json:"message_count"`
	LastMessageAt    *time.Time `db:"last_message_at" json:"last_message_at,omitempty"`
	Status           string    `db:"status" json:"status"` // "active" | "resolved" | "archived"
	CreatedAt        time.Time `db:"created_at" json:"created_at"`
}

// ThreadMatchResult is what the threading engine returns for each email.
type ThreadMatchResult struct {
	ThreadID   uuid.UUID `json:"thread_id"`
	ThreadKey  string    `json:"thread_key"`
	IsNewThread bool     `json:"is_new_thread"`
	MatchMethod string   `json:"match_method"` // "in_reply_to" | "references" | "fuzzy_subject" | "new"
}

// ============================================================================
// CONTACT — Output of Dedup, Used in Event Publisher
// ============================================================================

type Contact struct {
	ID               uuid.UUID       `json:"id"`
	UserID           uuid.UUID       `json:"user_id"`
	CanonicalEmail   string          `json:"canonical_email"`
	NameVariants     []string        `json:"name_variants"`
	Organization     *string         `json:"organization,omitempty"`
	FirstContactDate *time.Time      `json:"first_contact_date,omitempty"`
	LastContactDate  *time.Time      `json:"last_contact_date,omitempty"`
	InteractionCount int             `json:"interaction_count"`
	AvgResponseHours *float64        `json:"avg_response_hours,omitempty"`
	ToneHistory      []string        `json:"tone_history"`
	TotalMonetaryValue float64       `json:"total_monetary_value"`
	Projects         []string        `json:"projects"`
}

// DedupResult is what the contact dedup engine returns.
type DedupResult struct {
	ContactID     uuid.UUID   `json:"contact_id"`
	IsNewContact  bool        `json:"is_new_contact"`
	IsFuzzyMatch  bool        `json:"is_fuzzy_match"`
	SimilarToIDs  []uuid.UUID `json:"similar_to_ids,omitempty"` // if fuzzy, who are they similar to
}

// ============================================================================
// NATS EVENTS — Event Envelopes (shared contract with Classification Core)
// ============================================================================

// EmailIngestedEvent is published to NATS subject "email.ingested"
// after parsing, threading, dedup, and persistence are complete.
type EmailIngestedEvent struct {
	EventID           uuid.UUID   `json:"event_id"`
	UserID            uuid.UUID   `json:"user_id"`
	Source            string      `json:"source"` // "gmail" | "outlook"
	AccountID         uuid.UUID   `json:"account_id"`
	ThreadID          uuid.UUID   `json:"thread_id"`
	RawEmailID        uuid.UUID   `json:"raw_email_id"`
	S3URI             string      `json:"s3_uri"`
	HasAttachments    bool        `json:"has_attachments"`
	SenderEmail       string      `json:"sender_email"`
	ReceivedAt        time.Time   `json:"received_at"`
	ClassificationHint string     `json:"classification_hint"` // always "pending"
	ContactIDs        []uuid.UUID `json:"contact_ids"` // dedup results
}

// Subject names — shared constants. DO NOT CHANGE.
const (
	SubjectEmailIngested    = "email.ingested"
	SubjectEmailIngestedDLQ = "email.ingested.dlq"
	SubjectIntelligenceCompress = "intelligence.compress"
	SubjectExtractCompleted = "ExtractCompleted"
	SubjectAutoHandled      = "AutoHandled"
	SubjectCardCreated      = "sync.notify.CardCreated"
)

// ============================================================================
// OAUTH / TOKEN — Shared between OAuth Track and Crypto Track
// ============================================================================

// EncryptedToken is the wire format for tokens stored in PostgreSQL.
type EncryptedToken struct {
	Ciphertext []byte `json:"ciphertext"`
	Nonce      []byte `json:"nonce"`
	KeyID      string `json:"key_id"` // reference to KMS key version
}

// TokenPair holds the OAuth token state for an email account.
type TokenPair struct {
	RefreshToken   *EncryptedToken  `json:"refresh_token"`
	AccessToken    *EncryptedToken  `json:"access_token,omitempty"` // ephemeral, 15min TTL
	AccessTokenPlaintext *string    `json:"-"` // in-memory only, NEVER persisted
	ExpiresAt      *time.Time       `json:"expires_at,omitempty"`
	ScopeGranted   []string         `json:"scope_granted"`
}

// OAuthProvider is the interface both Google and Microsoft implement.
type OAuthProvider interface {
	// AuthURL returns the OAuth authorization URL for initiating the flow.
	AuthURL(state string, redirectURI string) string

	// Exchange exchanges the authorization code for tokens.
	Exchange(ctx context.Context, code string, redirectURI string) (*TokenPair, error)

	// Refresh uses the refresh token to get a new access token.
	Refresh(ctx context.Context, refreshToken string) (*TokenPair, error)

	// Revoke revokes the tokens.
	Revoke(ctx context.Context, token string) error

	// ValidateWebhook validates an incoming webhook push notification.
	ValidateWebhook(payload []byte, headers map[string]string) (*WebhookPayload, error)

	// FetchSentHistory retrieves sent emails for voice calibration.
	FetchSentHistory(ctx context.Context, accessToken string, daysBack int) ([]ParsedEmail, error)

	// SendEmail sends an email via the provider API.
	// Returns the provider's message ID and any error.
	SendEmail(ctx context.Context, accessToken string, req SendEmailRequest) (string, error)

	// Name returns the provider name.
	Name() string
}

// EmailProvider is the minimal interface for sending emails.
// Both GoogleProvider and MicrosoftProvider implement this interface.
type EmailProvider interface {
	// SendEmail sends an email via the provider API.
	// Returns the provider's message ID and any error.
	SendEmail(ctx context.Context, accessToken string, req SendEmailRequest) (string, error)

	// Name returns the provider name.
	Name() string
}

type WebhookPayload struct {
	MessageID  string    `json:"message_id"`
	HistoryID  string    `json:"history_id,omitempty"`  // Gmail
	DeltaLink  string    `json:"delta_link,omitempty"`  // Outlook
	ChangeType string    `json:"change_type"`           // "created" | "updated" | "deleted"
	ReceivedAt time.Time `json:"received_at"`
}

type SendEmailRequest struct {
	To          string   `json:"to"`
	Subject     string   `json:"subject"`
	BodyText    string   `json:"body_text"`
	BodyHTML    string   `json:"body_html,omitempty"`
	InReplyTo   *string  `json:"in_reply_to,omitempty"`
	References  []string `json:"references,omitempty"`
}

// ============================================================================
// RATE LIMITING — Shared between Polling Workers and Parser
// ============================================================================

// RateLimitStatus is checked before every API call.
type RateLimitStatus struct {
	Allowed   bool      `json:"allowed"`
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"reset_at"`
	Backoff   time.Duration `json:"backoff,omitempty"`
}

// GmailRateLimit: 250 quota units / user / second
// OutlookRateLimit: 10,000 requests / 10 minutes / app
const (
	GmailQuotaUnitsPerSecond = 250
	GmailGetCost             = 5
	GmailHistoryListCost     = 2
	OutlookRequestsPer10Min  = 10000
)

// ============================================================================
// JSONB HELPER — For PostgreSQL JSONB fields
// ============================================================================

type JSONB map[string]interface{}

func (j JSONB) Value() (driver.Value, error) {
	return json.Marshal(j)
}

func (j *JSONB) Scan(value interface{}) error {
	if value == nil {
		*j = nil
		return nil
	}
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, j)
	case string:
		return json.Unmarshal([]byte(v), j)
	default:
		return nil
	}
}

// ============================================================================
// ERROR TYPES — Shared across all components
// ============================================================================

// IngestionError is the base error type for the Ingestion Mesh.
type IngestionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	UserID  string `json:"user_id,omitempty"`
	Retry   bool   `json:"retry"`
}

func (e IngestionError) Error() string {
	return e.Message
}

// Common error codes.
const (
	ErrCodeOAuthExpired       = "oauth_expired"
	ErrCodeRateLimited        = "rate_limited"
	ErrCodeThreadingFailed    = "threading_failed"
	ErrCodeDedupFailed        = "dedup_failed"
	ErrCodeParseFailed        = "parse_failed"
	ErrCodeOCRFailed          = "ocr_failed"
	ErrCodeNATSPublishFailed  = "nats_publish_failed"
	ErrCodeWebhookInvalid     = "webhook_invalid"
	ErrCodeTokenDecryptFailed = "token_decrypt_failed"
)
