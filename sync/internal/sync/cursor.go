package sync

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ---------------------------------------------------------------------------
// VersionCursor — server version tracking and change queries
// ---------------------------------------------------------------------------

// VersionCursor manages the server_version counter for each user and
// queries for changes since a given version. The version is a monotonic
// integer that increments on every server-side mutation to a user's data.
type VersionCursor struct {
	db *sqlx.DB
}

// NewVersionCursor creates a new VersionCursor.
func NewVersionCursor(db *sqlx.DB) *VersionCursor {
	return &VersionCursor{db: db}
}

// GetCurrentVersion returns the current server_version for a user from the
// user_queues table. If the user has no queue record, returns 0 (no error).
func (c *VersionCursor) GetCurrentVersion(ctx context.Context, userID uuid.UUID) (int, error) {
	var version int
	err := c.db.GetContext(ctx, &version, `
		SELECT COALESCE(server_version, 0)
		FROM user_queues
		WHERE user_id = $1
	`, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("get current version: %w", err)
	}
	return version, nil
}

// ChangesSince holds the three categories of changes for a sync response.
type ChangesSince struct {
	NewCards     []models.DecisionCard // cards created since version
	UpdatedCards []models.DecisionCard // cards modified since version
	RemovedCards []uuid.UUID           // cards soft-deleted since version
}

// GetChangesSince returns all cards changed since the given client version.
// Cards are categorized as:
//
//   NewCards:     card_state = 'pending' and server_version > since — first time seen
//   UpdatedCards: card_state != 'pending' and server_version > since — state changed
//   RemovedCards: card_state IN ('sent', 'archived', 'expired') and server_version > since — to remove from client
//
// The returned cards are ordered by urgency_score DESC (so client processes
// highest-urgency cards first) and include full card data.
func (c *VersionCursor) GetChangesSince(ctx context.Context, userID uuid.UUID, since int) (*ChangesSince, error) {
	if since < 0 {
		since = 0
	}

	// Query all cards for this user whose server_version is greater than the
	// client's last_sync_version. We fetch everything and categorise in Go
	// to avoid multiple round-trips.
	var cards []models.DecisionCard
	err := c.db.SelectContext(ctx, &cards, `
		SELECT id, user_id, thread_id, source_account_id, card_state,
			   from_field, they_want, context, need_from_user, chunk_citations,
			   urgency_score, auto_handle_rule_id, classification_confidence,
			   suggested_deadline, user_decided_at, sent_at,
			   server_version, created_at, updated_at
		FROM decision_cards
		WHERE user_id = $1 AND server_version > $2
		ORDER BY urgency_score DESC, created_at ASC
	`, userID, since)
	if err != nil {
		return nil, fmt.Errorf("get changes since: %w", err)
	}

	result := &ChangesSince{
		NewCards:     make([]models.DecisionCard, 0),
		UpdatedCards: make([]models.DecisionCard, 0),
		RemovedCards: make([]uuid.UUID, 0),
	}

	for _, card := range cards {
		// Categorise based on card state:
		// - Terminal states → client should remove the card from its queue
		// - Pending → either new or updated (if version == 1, it's new)
		if IsTerminal(card.CardState) {
			result.RemovedCards = append(result.RemovedCards, card.ID)
		} else if card.ServerVersion == 1 {
			// First version = newly created card
			result.NewCards = append(result.NewCards, card)
		} else {
			// Version > 1 and non-terminal = state update
			result.UpdatedCards = append(result.UpdatedCards, card)
		}
	}

	return result, nil
}

// IncrementVersion atomically increments the server_version for a user and
// returns the new value. If the user has no queue row, one is created with
// version = 1. This is the ONLY way server_version should be incremented.
func (c *VersionCursor) IncrementVersion(ctx context.Context, userID uuid.UUID) (int, error) {
	now := time.Now().UTC()

	var version int
	err := c.db.GetContext(ctx, &version, `
		INSERT INTO user_queues (user_id, pending_count, server_version, created_at, updated_at)
		VALUES ($1, 0, 1, $2, $2)
		ON CONFLICT (user_id) DO UPDATE SET
			server_version = user_queues.server_version + 1,
			updated_at = EXCLUDED.updated_at
		RETURNING server_version
	`, userID, now)
	if err != nil {
		return 0, fmt.Errorf("increment version: %w", err)
	}
	return version, nil
}

// BumpVersionTx increments the server version inside an existing transaction.
// Use this when the version bump is part of a larger atomic operation.
func (c *VersionCursor) BumpVersionTx(ctx context.Context, tx *sqlx.Tx, userID uuid.UUID) (int, error) {
	now := time.Now().UTC()

	var version int
	err := tx.GetContext(ctx, &version, `
		INSERT INTO user_queues (user_id, pending_count, server_version, created_at, updated_at)
		VALUES ($1, 0, 1, $2, $2)
		ON CONFLICT (user_id) DO UPDATE SET
			server_version = user_queues.server_version + 1,
			updated_at = EXCLUDED.updated_at
		RETURNING server_version
	`, userID, now)
	if err != nil {
		return 0, fmt.Errorf("bump version tx: %w", err)
	}
	return version, nil
}

// EnsureUserQueue ensures a user_queues row exists for the given user.
// This is idempotent — safe to call multiple times.
func (c *VersionCursor) EnsureUserQueue(ctx context.Context, userID uuid.UUID) error {
	now := time.Now().UTC()
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO user_queues (user_id, pending_count, server_version, created_at, updated_at)
		VALUES ($1, 0, 0, $2, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, userID, now)
	if err != nil {
		return fmt.Errorf("ensure user queue: %w", err)
	}
	return nil
}
