package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ---------------------------------------------------------------------------
// SyncStore — sync_log persistence and card/draft queries
// ---------------------------------------------------------------------------

// SyncStore handles persistence for sync operations: the sync_log audit trail,
// card lookups with ownership verification, and draft queries needed by the
// CRDT merge engine.
type SyncStore struct {
	db *sqlx.DB
}

// NewSyncStore creates a new SyncStore.
func NewSyncStore(db *sqlx.DB) *SyncStore {
	return &SyncStore{db: db}
}

// ---------------------------------------------------------------------------
// sync_log — audit trail for every sync operation
// ---------------------------------------------------------------------------

// SyncLogEntry records a single sync event (accepted change, rejected change,
// or full sync session) for debugging and audit purposes.
type SyncLogEntry struct {
	ID         uuid.UUID       `db:"id" json:"id"`
	UserID     uuid.UUID       `db:"user_id" json:"user_id"`
	DeviceID   string          `db:"device_id" json:"device_id"`
	CardID     *uuid.UUID      `db:"card_id" json:"card_id,omitempty"`
	Operation  string          `db:"operation" json:"operation"` // "accept", "reject", "sync_start", "sync_complete"
	ChangeType string          `db:"change_type" json:"change_type"` // "approve", "edit", "consult", ""
	Reason     string          `db:"reason" json:"reason"`
	ServerVersion int          `db:"server_version" json:"server_version"`
	Details    json.RawMessage `db:"details" json:"details,omitempty"`
	CreatedAt  time.Time       `db:"created_at" json:"created_at"`
}

// LogChange records a single accepted or rejected change to the sync_log.
func (s *SyncStore) LogChange(ctx context.Context, entry *SyncLogEntry) error {
	if s.db == nil {
		return fmt.Errorf("no database connection")
	}
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}

	query := `
		INSERT INTO sync_log (
			id, user_id, device_id, card_id, operation, change_type,
			reason, server_version, details, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := s.db.ExecContext(ctx, query,
		entry.ID, entry.UserID, entry.DeviceID, entry.CardID,
		entry.Operation, entry.ChangeType, entry.Reason,
		entry.ServerVersion, entry.Details, entry.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("log sync change: %w", err)
	}
	return nil
}

// LogChangeTx records a sync log entry inside a transaction.
func (s *SyncStore) LogChangeTx(ctx context.Context, tx *sqlx.Tx, entry *SyncLogEntry) error {
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}

	query := `
		INSERT INTO sync_log (
			id, user_id, device_id, card_id, operation, change_type,
			reason, server_version, details, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := tx.ExecContext(ctx, query,
		entry.ID, entry.UserID, entry.DeviceID, entry.CardID,
		entry.Operation, entry.ChangeType, entry.Reason,
		entry.ServerVersion, entry.Details, entry.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("log sync change tx: %w", err)
	}
	return nil
}

// LogSessionStart records the beginning of a sync session.
func (s *SyncStore) LogSessionStart(ctx context.Context, userID uuid.UUID, deviceID string, clientVersion int) error {
	details, _ := json.Marshal(map[string]interface{}{
		"client_version": clientVersion,
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
	})
	return s.LogChange(ctx, &SyncLogEntry{
		UserID:        userID,
		DeviceID:      deviceID,
		Operation:     "sync_start",
		ChangeType:    "",
		Reason:        "",
		ServerVersion: clientVersion,
		Details:       details,
	})
}

// ---------------------------------------------------------------------------
// Card queries — ownership-aware lookups for CRDT merge
// ---------------------------------------------------------------------------

// GetCardByID retrieves a card by ID. Returns sql.ErrNoRows equivalent if
// the card does not exist.
func (s *SyncStore) GetCardByID(ctx context.Context, cardID uuid.UUID) (*models.DecisionCard, error) {
	if s.db == nil {
		return nil, fmt.Errorf("no database connection")
	}
	var card models.DecisionCard
	query := `
		SELECT id, user_id, thread_id, source_account_id, card_state,
			   from_field, they_want, context, need_from_user, chunk_citations,
			   urgency_score, auto_handle_rule_id, classification_confidence,
			   suggested_deadline, user_decided_at, sent_at,
			   server_version, created_at, updated_at
		FROM decision_cards
		WHERE id = $1
	`
	if err := s.db.GetContext(ctx, &card, query, cardID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("card not found: %s", cardID)
		}
		return nil, fmt.Errorf("get card by id: %w", err)
	}
	return &card, nil
}

// GetCardOwnedBy retrieves a card by ID and verifies it belongs to the user.
func (s *SyncStore) GetCardOwnedBy(ctx context.Context, cardID, userID uuid.UUID) (*models.DecisionCard, error) {
	if s.db == nil {
		return nil, fmt.Errorf("no database connection")
	}
	var card models.DecisionCard
	query := `
		SELECT id, user_id, thread_id, source_account_id, card_state,
			   from_field, they_want, context, need_from_user, chunk_citations,
			   urgency_score, auto_handle_rule_id, classification_confidence,
			   suggested_deadline, user_decided_at, sent_at,
			   server_version, created_at, updated_at
		FROM decision_cards
		WHERE id = $1 AND user_id = $2
	`
	if err := s.db.GetContext(ctx, &card, query, cardID, userID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("card not found or not owned by user: %s", cardID)
		}
		return nil, fmt.Errorf("get card owned by: %w", err)
	}
	return &card, nil
}

// UpdateCardState atomically updates a card's state and bumps its server_version.
func (s *SyncStore) UpdateCardState(ctx context.Context, cardID uuid.UUID, newState string) error {
	if s.db == nil {
		return fmt.Errorf("no database connection")
	}
	validStates := map[string]bool{
		"pending":    true,
		"consulting": true,
		"drafting":   true,
		"approved":   true,
		"sent":       true,
		"archived":   true,
		"expired":    true,
	}
	if !validStates[newState] {
		return fmt.Errorf("invalid card state: %s", newState)
	}

	query := `
		UPDATE decision_cards
		SET card_state = $1, server_version = server_version + 1, updated_at = $2
		WHERE id = $3
	`
	res, err := s.db.ExecContext(ctx, query, newState, time.Now().UTC(), cardID)
	if err != nil {
		return fmt.Errorf("update card state: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("card not found: %s", cardID)
	}
	return nil
}

// UpdateCardStateTx updates card state inside a transaction.
func (s *SyncStore) UpdateCardStateTx(ctx context.Context, tx *sqlx.Tx, cardID uuid.UUID, newState string) error {
	query := `
		UPDATE decision_cards
		SET card_state = $1, server_version = server_version + 1, updated_at = $2
		WHERE id = $3
	`
	res, err := tx.ExecContext(ctx, query, newState, time.Now().UTC(), cardID)
	if err != nil {
		return fmt.Errorf("update card state tx: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("card not found: %s", cardID)
	}
	return nil
}

// MarkCardApproved atomically marks a card as approved and records the decision time.
func (s *SyncStore) MarkCardApproved(ctx context.Context, cardID uuid.UUID) error {
	if s.db == nil {
		return fmt.Errorf("no database connection")
	}
	query := `
		UPDATE decision_cards
		SET card_state = 'approved',
		    user_decided_at = $1,
		    server_version = server_version + 1,
		    updated_at = $1
		WHERE id = $2
	`
	res, err := s.db.ExecContext(ctx, query, time.Now().UTC(), cardID)
	if err != nil {
		return fmt.Errorf("mark card approved: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("card not found: %s", cardID)
	}
	return nil
}

// MarkCardApprovedTx marks a card as approved inside a transaction.
func (s *SyncStore) MarkCardApprovedTx(ctx context.Context, tx *sqlx.Tx, cardID uuid.UUID) error {
	query := `
		UPDATE decision_cards
		SET card_state = 'approved',
		    user_decided_at = $1,
		    server_version = server_version + 1,
		    updated_at = $1
		WHERE id = $2
	`
	res, err := tx.ExecContext(ctx, query, time.Now().UTC(), cardID)
	if err != nil {
		return fmt.Errorf("mark card approved tx: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("card not found: %s", cardID)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Draft queries for CRDT merge
// ---------------------------------------------------------------------------

// GetDraftByID retrieves a draft by ID.
func (s *SyncStore) GetDraftByID(ctx context.Context, draftID uuid.UUID) (*models.Draft, error) {
	if s.db == nil {
		return nil, fmt.Errorf("no database connection")
	}
	var draft models.Draft
	query := `
		SELECT id, card_id, user_id, thread_id, draft_body, subject_line,
			   tone_profile, in_reply_to, references, model_used, tokens_used,
			   user_approved, sent_at, created_at
		FROM drafts
		WHERE id = $1
	`
	if err := s.db.GetContext(ctx, &draft, query, draftID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("draft not found: %s", draftID)
		}
		return nil, fmt.Errorf("get draft by id: %w", err)
	}
	return &draft, nil
}

// ApproveDraft marks a draft as user-approved.
func (s *SyncStore) ApproveDraft(ctx context.Context, draftID uuid.UUID) error {
	if s.db == nil {
		return fmt.Errorf("no database connection")
	}
	query := `
		UPDATE drafts
		SET user_approved = true, updated_at = $1
		WHERE id = $2
	`
	res, err := s.db.ExecContext(ctx, query, time.Now().UTC(), draftID)
	if err != nil {
		return fmt.Errorf("approve draft: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("draft not found: %s", draftID)
	}
	return nil
}

// ApproveDraftTx marks a draft as user-approved inside a transaction.
func (s *SyncStore) ApproveDraftTx(ctx context.Context, tx *sqlx.Tx, draftID uuid.UUID) error {
	query := `
		UPDATE drafts
		SET user_approved = true, updated_at = $1
		WHERE id = $2
	`
	res, err := tx.ExecContext(ctx, query, time.Now().UTC(), draftID)
	if err != nil {
		return fmt.Errorf("approve draft tx: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("draft not found: %s", draftID)
	}
	return nil
}

// GetLatestDraftForCard returns the most recent draft for a card.
func (s *SyncStore) GetLatestDraftForCard(ctx context.Context, cardID uuid.UUID) (*models.Draft, error) {
	if s.db == nil {
		return nil, fmt.Errorf("no database connection")
	}
	var draft models.Draft
	query := `
		SELECT id, card_id, user_id, thread_id, draft_body, subject_line,
			   tone_profile, in_reply_to, references, model_used, tokens_used,
			   user_approved, sent_at, created_at
		FROM drafts
		WHERE card_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`
	if err := s.db.GetContext(ctx, &draft, query, cardID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no drafts found for card: %s", cardID)
		}
		return nil, fmt.Errorf("get latest draft: %w", err)
	}
	return &draft, nil
}

// ---------------------------------------------------------------------------
// Transaction helper
// ---------------------------------------------------------------------------

// WithTx executes a function within a database transaction.
// Automatically rolls back on error, commits on success.
func (s *SyncStore) WithTx(ctx context.Context, fn func(*sqlx.Tx) error) error {
	if s.db == nil {
		return fmt.Errorf("no database connection")
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			// Log rollback error but return original error
			_ = rbErr
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
