// Package models defines the shared data structures for Sync & State.
// These are the wire contracts between Sync API, client, and other bounded contexts.
// DO NOT MODIFY without coordination.
package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ============================================================================
// DECISION CARD — Server-side representation (mirrors client types)
// ============================================================================

type DecisionCard struct {
	ID                     uuid.UUID       `db:"id" json:"id"`
	UserID                 uuid.UUID       `db:"user_id" json:"user_id"`
	ThreadID               uuid.UUID       `db:"thread_id" json:"thread_id"`
	SourceAccountID        uuid.UUID       `db:"source_account_id" json:"source_account_id"`
	CardState              string          `db:"card_state" json:"card_state"` // pending, consulting, drafting, approved, sent, archived, expired
	FromField              json.RawMessage `db:"from_field" json:"from"`
	TheyWant               string          `db:"they_want" json:"they_want"`
	Context                json.RawMessage `db:"context" json:"context"`
	NeedFromUser           string          `db:"need_from_user" json:"need_from_user"`
	ChunkCitations         json.RawMessage `db:"chunk_citations" json:"chunk_citations"`
	UrgencyScore           float64         `db:"urgency_score" json:"urgency_score"`
	AutoHandleRuleID       *uuid.UUID      `db:"auto_handle_rule_id" json:"auto_handle_rule_id,omitempty"`
	ClassificationConfidence *float64      `db:"classification_confidence" json:"classification_confidence,omitempty"`
	SuggestedDeadline      *time.Time      `db:"suggested_deadline" json:"suggested_deadline,omitempty"`
	UserDecidedAt          *time.Time      `db:"user_decided_at" json:"user_decided_at,omitempty"`
	SentAt                 *time.Time      `db:"sent_at" json:"sent_at,omitempty"`
	ServerVersion          int             `db:"server_version" json:"server_version"` // incremented on every server-side change
	CreatedAt              time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt              time.Time       `db:"updated_at" json:"updated_at"`
}

// ============================================================================
// DRAFT — Server-side representation
// ============================================================================

type Draft struct {
	ID          uuid.UUID  `db:"id" json:"id"`
	CardID      uuid.UUID  `db:"card_id" json:"card_id"`
	UserID      uuid.UUID  `db:"user_id" json:"user_id"`
	ThreadID    uuid.UUID  `db:"thread_id" json:"thread_id"`
	DraftBody   string     `db:"draft_body" json:"draft_body"`
	SubjectLine *string    `db:"subject_line" json:"subject_line,omitempty"`
	ToneProfile *string    `db:"tone_profile" json:"tone_profile,omitempty"`
	InReplyTo   *string    `db:"in_reply_to" json:"in_reply_to,omitempty"`
	References  []string   `db:"references" json:"references"`
	ModelUsed   *string    `db:"model_used" json:"model_used,omitempty"`
	TokensUsed  *int       `db:"tokens_used" json:"tokens_used,omitempty"`
	UserApproved bool      `db:"user_approved" json:"user_approved"`
	SentAt      *time.Time `db:"sent_at" json:"sent_at,omitempty"`
	CreatedAt   time.Time  `db:"created_at" json:"created_at"`
}

// ============================================================================
// SYNC PROTOCOL — Wire format between client and server
// ============================================================================

type SyncRequest struct {
	DeviceID         string          `json:"device_id"`
	LastSyncVersion  int             `json:"last_sync_version"`
	LocalChanges     []LocalChange   `json:"local_changes"`
}

type LocalChange struct {
	CardID           uuid.UUID `json:"card_id"`
	Version          int       `json:"version"` // client's version number
	State            string    `json:"state"`
	Decision         *string   `json:"decision,omitempty"`
	DraftBody        *string   `json:"draft_body,omitempty"`
	ApprovedDraftID  *uuid.UUID `json:"approved_draft_id,omitempty"`
}

type SyncResponse struct {
	ServerVersion    int              `json:"server_version"`
	AcceptedChanges  []uuid.UUID      `json:"accepted_changes"`  // card IDs accepted
	RejectedChanges  []RejectedChange `json:"rejected_changes"`
	NewCards         []DecisionCard   `json:"new_cards"`
	UpdatedCards     []DecisionCard   `json:"updated_cards"`
	RemovedCards     []uuid.UUID      `json:"removed_cards"` // card IDs to remove
}

type RejectedChange struct {
	CardID       uuid.UUID `json:"card_id"`
	Reason       string    `json:"reason"`
	ServerState  string    `json:"server_state"`
}

// ============================================================================
// BATCH
// ============================================================================

type BatchInfo struct {
	Size                      int            `json:"size"`
	EstimatedClearTimeMinutes int            `json:"estimated_clear_time_minutes"`
	Cards                     []DecisionCard `json:"cards"` // ordered by urgency desc
}

// ============================================================================
// DECISION ACTIONS
// ============================================================================

type DecideRequest struct {
	CardID   uuid.UUID `json:"card_id"`
	Decision string    `json:"decision"` // "approve", "edit", "consult"
	Input    *string   `json:"input,omitempty"`
}

type DecideResponse struct {
	DraftID     uuid.UUID `json:"draft_id"`
	DraftBody   string    `json:"draft_body"`
	SubjectLine *string   `json:"subject_line,omitempty"`
}

// ============================================================================
// CONSULTATION
// ============================================================================

type ConsultRequest struct {
	CardID   uuid.UUID `json:"card_id"`
	Question string    `json:"question"`
}

type ConsultResponse struct {
	Answer          string          `json:"answer"`
	Citations       []ChunkCitation `json:"citations"`
	TurnsRemaining  int             `json:"turns_remaining"`
}

type ChunkCitation struct {
	ChunkID         uuid.UUID `json:"chunk_id"`
	VerbatimSnippet string    `json:"verbatim_snippet"`
	EmailID         uuid.UUID `json:"email_id"`
	ParagraphIndex  int       `json:"paragraph_index"`
}

// ============================================================================
// AUTH / SESSION
// ============================================================================

type DeviceSession struct {
	ID           uuid.UUID `db:"id" json:"id"`
	UserID       uuid.UUID `db:"user_id" json:"user_id"`
	DeviceID     string    `db:"device_id" json:"device_id"`
	DeviceType   string    `db:"device_type" json:"device_type"` // "ios", "android"
	DeviceName   string    `db:"device_name" json:"device_name"`
	FCMToken     *string   `db:"fcm_token" json:"fcm_token,omitempty"`
	APNSToken    *string   `db:"apns_token" json:"apns_token,omitempty"`
	LastActiveAt time.Time `db:"last_active_at" json:"last_active_at"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
}

// RefreshToken is the server-side storage of a hashed refresh token for JWT rotation.
// One row per (user_id, device_id) — upsert on rotation.
type RefreshToken struct {
	ID        uuid.UUID `db:"id" json:"id"`
	UserID    uuid.UUID `db:"user_id" json:"user_id"`
	DeviceID  string    `db:"device_id" json:"device_id"`
	TokenHash string    `db:"token_hash" json:"-"`
	ExpiresAt time.Time `db:"expires_at" json:"expires_at"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token"`
	DeviceID     string `json:"device_id"`
}

type TokenResponse struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// ============================================================================
// NOTIFICATION
// ============================================================================

type Notification struct {
	ID        uuid.UUID `db:"id" json:"id"`
	UserID    uuid.UUID `db:"user_id" json:"user_id"`
	Type      string    `db:"type" json:"type"` // "batch", "interrupt", "temporal", "staging"
	Title     string    `db:"title" json:"title"`
	Body      string    `db:"body" json:"body"`
	Data      json.RawMessage `db:"data" json:"data,omitempty"` // card_id, thread_id, etc.
	SentAt    *time.Time `db:"sent_at" json:"sent_at,omitempty"`
	ReadAt    *time.Time `db:"read_at" json:"read_at,omitempty"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// NotificationPreference holds per-user notification settings.
type NotificationPreference struct {
	UserID           uuid.UUID `db:"user_id" json:"user_id"`
	QuietHoursStart  int       `db:"quiet_hours_start" json:"quiet_hours_start"` // 0-23, default 22
	QuietHoursEnd    int       `db:"quiet_hours_end" json:"quiet_hours_end"`     // 0-23, default 7
	BatchThreshold   int       `db:"batch_threshold" json:"batch_threshold"`     // default 5
	DailyDigestTime  string    `db:"daily_digest_time" json:"daily_digest_time"` // "08:00"
	InterruptEnabled bool      `db:"interrupt_enabled" json:"interrupt_enabled"` // true
	CreatedAt        time.Time `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time `db:"updated_at" json:"updated_at"`
}

type BatchNotificationPayload struct {
	BatchSize                int `json:"batch_size"`
	EstimatedClearTimeMinutes int `json:"estimated_clear_time_minutes"`
}

type InterruptNotificationPayload struct {
	CardID      uuid.UUID `json:"card_id"`
	SenderName  string    `json:"sender_name"`
	AtomicAsk   string    `json:"atomic_ask"`
	UrgencyScore float64  `json:"urgency_score"`
}

// ============================================================================
// QUEUE MANAGEMENT
// ============================================================================

type UserQueue struct {
	UserID            uuid.UUID `db:"user_id" json:"user_id"`
	PendingCount      int       `db:"pending_count" json:"pending_count"`
	ServerVersion     int       `db:"server_version" json:"server_version"`
	LastNotificationAt *time.Time `db:"last_notification_at" json:"last_notification_at,omitempty"`
	CreatedAt         time.Time `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time `db:"updated_at" json:"updated_at"`
}

// ============================================================================
// WEBSOCKET — Sending Session
// ============================================================================

type WSEventType string

const (
	WSEventSpawn     WSEventType = "spawn"
	WSEventParagraph WSEventType = "paragraph"
	WSEventAccept    WSEventType = "accept"
	WSEventEdit      WSEventType = "edit"
	WSEventDelegate  WSEventType = "delegate"
	WSEventPing      WSEventType = "ping"
	WSEventPong      WSEventType = "pong"
)

type WSEvent struct {
	Type         WSEventType     `json:"type"`
	CardID       uuid.UUID       `json:"card_id"`
	Text         string          `json:"text,omitempty"`         // for paragraph
	TriggerWord  string          `json:"trigger_word,omitempty"` // for spawn
	CursorPosition int           `json:"cursor_position,omitempty"`
	Timestamp    time.Time       `json:"timestamp"`
}

// ============================================================================
// CALENDAR
// ============================================================================

type CalendarEvent struct {
	ID               uuid.UUID `db:"id" json:"id"`
	UserID           uuid.UUID `db:"user_id" json:"user_id"`
	SourceAccountID  uuid.UUID `db:"source_account_id" json:"source_account_id"`
	ExternalEventID  string    `db:"external_event_id" json:"external_event_id"`
	ThreadID         *uuid.UUID `db:"thread_id" json:"thread_id,omitempty"`
	Title            string    `db:"title" json:"title"`
	StartAt          time.Time `db:"start_at" json:"start_at"`
	EndAt            time.Time `db:"end_at" json:"end_at"`
	Timezone         *string   `db:"timezone" json:"timezone,omitempty"`
	Location         *string   `db:"location" json:"location,omitempty"`
	AttendeeEmails   []string  `db:"attendee_emails" json:"attendee_emails"`
	Description      *string   `db:"description" json:"description,omitempty"`
	IsConfirmed      bool      `db:"is_confirmed" json:"is_confirmed"`
	ReminderSentAt   *time.Time `db:"reminder_sent_at" json:"reminder_sent_at,omitempty"`
	BriefingCardID   *uuid.UUID `db:"briefing_card_id" json:"briefing_card_id,omitempty"`
	CreatedAt        time.Time `db:"created_at" json:"created_at"`
}

// ============================================================================
// REMINDER
// ============================================================================

type ReminderJob struct {
	ID            uuid.UUID `db:"id" json:"id"`
	UserID        uuid.UUID `db:"user_id" json:"user_id"`
	EventID       uuid.UUID `db:"event_id" json:"event_id"`
	ReminderType  string    `db:"reminder_type" json:"reminder_type"` // "pre_event", "daily_digest", "conflict_alert"
	ScheduledFor  time.Time `db:"scheduled_for" json:"scheduled_for"`
	ProcessedAt   *time.Time `db:"processed_at" json:"processed_at,omitempty"`
	CreatedAt     time.Time `db:"created_at" json:"created_at"`
}

// ============================================================================
// ERROR TYPES
// ============================================================================

type SyncError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Retry   bool   `json:"retry"`
}

func (e SyncError) Error() string { return e.Message }

const (
	ErrCodeAuthExpired     = "auth_expired"
	ErrCodeVersionConflict = "version_conflict"
	ErrCodeCardNotFound    = "card_not_found"
	ErrCodeDraftNotFound   = "draft_not_found"
	ErrCodeQueueEmpty      = "queue_empty"
	ErrCodeRateLimited     = "rate_limited"
)
