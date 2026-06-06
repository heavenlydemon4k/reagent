// Package batch provides the batch management API — server-side queue that accumulates
// decision cards and delivers them to clients as batches. This is the "batch clearing
// only" model's server component.
package batch

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// CardStore provides PostgreSQL persistence for decision cards and user queues.
type CardStore struct {
	db *sqlx.DB
}

// NewCardStore creates a new CardStore backed by PostgreSQL.
func NewCardStore(db *sqlx.DB) *CardStore {
	return &CardStore{db: db}
}

// Insert persists a new decision card. The card must have all required fields set.
// Uses a transaction to insert the card and ensure the user_queues row exists.
func (s *CardStore) Insert(ctx context.Context, card *models.DecisionCard) error {
	if card.ID == uuid.Nil {
		card.ID = uuid.New()
	}
	now := time.Now().UTC()
	if card.CreatedAt.IsZero() {
		card.CreatedAt = now
	}
	if card.UpdatedAt.IsZero() {
		card.UpdatedAt = now
	}
	if card.CardState == "" {
		card.CardState = "pending"
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for card insert: %w", err)
	}
	defer tx.Rollback()

	query := `
		INSERT INTO decision_cards (
			id, user_id, thread_id, source_account_id, card_state,
			from_field, they_want, context, need_from_user, chunk_citations,
			urgency_score, auto_handle_rule_id, classification_confidence,
			suggested_deadline, user_decided_at, sent_at, server_version,
			created_at, updated_at
		) VALUES (
			:id, :user_id, :thread_id, :source_account_id, :card_state,
			:from_field, :they_want, :context, :need_from_user, :chunk_citations,
			:urgency_score, :auto_handle_rule_id, :classification_confidence,
			:suggested_deadline, :user_decided_at, :sent_at, :server_version,
			:created_at, :updated_at
		)
	`
	_, err = tx.NamedExecContext(ctx, query, card)
	if err != nil {
		return fmt.Errorf("insert decision card: %w", err)
	}

	// Ensure user_queues row exists — upsert with conflict handling.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO user_queues (user_id, pending_count, server_version, created_at, updated_at)
		VALUES ($1, 0, 0, $2, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, card.UserID, now)
	if err != nil {
		return fmt.Errorf("ensure user queue: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit card insert: %w", err)
	}

	return nil
}

// GetPending returns pending decision cards for a user ordered by urgency_score
// DESC, then created_at ASC. Returns at most limit cards (limit <= 0 means no limit).
func (s *CardStore) GetPending(ctx context.Context, userID uuid.UUID, limit int) ([]models.DecisionCard, error) {
	var cards []models.DecisionCard
	query := `
		SELECT *
		FROM decision_cards
		WHERE user_id = $1 AND card_state = 'pending'
		ORDER BY urgency_score DESC, created_at ASC
	`
	args := []interface{}{userID}
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", len(args)+1)
		args = append(args, limit)
	}

	if err := s.db.SelectContext(ctx, &cards, query, args...); err != nil {
		return nil, fmt.Errorf("select pending cards: %w", err)
	}
	return cards, nil
}

// GetPendingOrdered returns pending cards with the same ordering guarantee.
// Alias for GetPending with explicit naming for clarity in queue logic.
func (s *CardStore) GetPendingOrdered(ctx context.Context, userID uuid.UUID, limit int) ([]models.DecisionCard, error) {
	return s.GetPending(ctx, userID, limit)
}

// UpdateState atomically updates a card's state and sets the updated_at timestamp.
// Valid states: pending, consulting, drafting, approved, sent, archived, expired.
func (s *CardStore) UpdateState(ctx context.Context, cardID uuid.UUID, state string) error {
	validStates := map[string]bool{
		"pending":    true,
		"consulting": true,
		"drafting":   true,
		"approved":   true,
		"sent":       true,
		"archived":   true,
		"expired":    true,
	}
	if !validStates[state] {
		return fmt.Errorf("invalid card state: %s", state)
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE decision_cards
		SET card_state = $1, updated_at = $2
		WHERE id = $3
	`, state, time.Now().UTC(), cardID)
	if err != nil {
		return fmt.Errorf("update card state: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("card not found: %s", cardID)
	}

	return nil
}

// GetByID retrieves a single decision card by its UUID.
func (s *CardStore) GetByID(ctx context.Context, cardID uuid.UUID) (*models.DecisionCard, error) {
	var card models.DecisionCard
	if err := s.db.GetContext(ctx, &card, `SELECT * FROM decision_cards WHERE id = $1`, cardID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("card not found: %s", cardID)
		}
		return nil, fmt.Errorf("get card by id: %w", err)
	}
	return &card, nil
}

// GetPendingCount returns the number of pending cards for a user.
func (s *CardStore) GetPendingCount(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	if err := s.db.GetContext(ctx, &count, `
		SELECT COUNT(*) FROM decision_cards
		WHERE user_id = $1 AND card_state = 'pending'
	`, userID); err != nil {
		return 0, fmt.Errorf("count pending cards: %w", err)
	}
	return count, nil
}

// GetUrgentCount returns the number of pending cards with urgency_score >= threshold.
func (s *CardStore) GetUrgentCount(ctx context.Context, userID uuid.UUID, threshold float64) (int, error) {
	var count int
	if err := s.db.GetContext(ctx, &count, `
		SELECT COUNT(*) FROM decision_cards
		WHERE user_id = $1 AND card_state = 'pending' AND urgency_score >= $2
	`, userID, threshold); err != nil {
		return 0, fmt.Errorf("count urgent cards: %w", err)
	}
	return count, nil
}

// ---------------------------------------------------------------------------
// USER QUEUE OPERATIONS
// ---------------------------------------------------------------------------

// IncrementServerVersion atomically increments the server_version for a user's
// queue and returns the new version number. Creates the row if absent.
func (s *CardStore) IncrementServerVersion(ctx context.Context, userID uuid.UUID) (int, error) {
	now := time.Now().UTC()

	var version int
	err := s.db.GetContext(ctx, &version, `
		INSERT INTO user_queues (user_id, pending_count, server_version, created_at, updated_at)
		VALUES ($1, 0, 1, $2, $2)
		ON CONFLICT (user_id) DO UPDATE SET
			server_version = user_queues.server_version + 1,
			updated_at = EXCLUDED.updated_at
		RETURNING server_version
	`, userID, now)
	if err != nil {
		return 0, fmt.Errorf("increment server version: %w", err)
	}
	return version, nil
}

// GetServerVersion returns the current server version for a user.
func (s *CardStore) GetServerVersion(ctx context.Context, userID uuid.UUID) (int, error) {
	var version int
	err := s.db.GetContext(ctx, &version, `
		SELECT COALESCE(server_version, 0)
		FROM user_queues
		WHERE user_id = $1
	`, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("get server version: %w", err)
	}
	return version, nil
}

// IncrementPendingCount atomically increments pending_count for a user.
func (s *CardStore) IncrementPendingCount(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE user_queues
		SET pending_count = pending_count + 1, updated_at = $2
		WHERE user_id = $1
	`, userID, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("increment pending count: %w", err)
	}
	return nil
}

// DecrementPendingCount atomically decrements pending_count for a user.
func (s *CardStore) DecrementPendingCount(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE user_queues
		SET pending_count = GREATEST(pending_count - 1, 0), updated_at = $2
		WHERE user_id = $1
	`, userID, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("decrement pending count: %w", err)
	}
	return nil
}

// GetUserQueue retrieves the full user queue record.
func (s *CardStore) GetUserQueue(ctx context.Context, userID uuid.UUID) (*models.UserQueue, error) {
	var uq models.UserQueue
	err := s.db.GetContext(ctx, &uq, `SELECT * FROM user_queues WHERE user_id = $1`, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return &models.UserQueue{
				UserID:        userID,
				PendingCount:  0,
				ServerVersion: 0,
			}, nil
		}
		return nil, fmt.Errorf("get user queue: %w", err)
	}
	return &uq, nil
}

// MarkCardSent updates a card's state to 'sent' and records the sent_at timestamp
// within a transaction, also decrementing the user's pending count.
func (s *CardStore) MarkCardSent(ctx context.Context, userID uuid.UUID, cardID uuid.UUID) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for mark sent: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()

	_, err = tx.ExecContext(ctx, `
		UPDATE decision_cards
		SET card_state = 'sent', sent_at = $1, updated_at = $1
		WHERE id = $2 AND user_id = $3
	`, now, cardID, userID)
	if err != nil {
		return fmt.Errorf("update card to sent: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE user_queues
		SET pending_count = GREATEST(pending_count - 1, 0),
		    server_version = server_version + 1,
		    updated_at = $2
		WHERE user_id = $1
	`, userID, now)
	if err != nil {
		return fmt.Errorf("decrement pending count on sent: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mark card sent: %w", err)
	}

	return nil
}

// RecordDismissal inserts a notification dismissal record for analytics.
func (s *CardStore) RecordDismissal(ctx context.Context, userID uuid.UUID, dismissedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_dismissals (id, user_id, dismissed_at, created_at)
		VALUES ($1, $2, $3, $4)
	`, uuid.New(), userID, dismissedAt, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("record dismissal: %w", err)
	}
	return nil
}
