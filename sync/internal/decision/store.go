// Package decision provides the decision processing API for the Sync & State bounded context.
package decision

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

// ErrCardNotFound indicates the requested decision card does not exist.
type ErrCardNotFound struct{ CardID uuid.UUID }

func (e ErrCardNotFound) Error() string { return fmt.Sprintf("card %s not found", e.CardID) }

// ErrDraftNotFound indicates the requested draft does not exist.
type ErrDraftNotFound struct{ DraftID uuid.UUID }

func (e ErrDraftNotFound) Error() string { return fmt.Sprintf("draft %s not found", e.DraftID) }

// ErrCardOwnership indicates the user does not own the card.
type ErrCardOwnership struct{ CardID, UserID uuid.UUID }

func (e ErrCardOwnership) Error() string {
	return fmt.Sprintf("card %s does not belong to user %s", e.CardID, e.UserID)
}

// ErrAlreadyApproved indicates the draft has already been approved.
type ErrAlreadyApproved struct{ DraftID uuid.UUID }

func (e ErrAlreadyApproved) Error() string { return fmt.Sprintf("draft %s already approved", e.DraftID) }

// ErrAlreadySent indicates the draft has already been sent.
type ErrAlreadySent struct{ DraftID uuid.UUID }

func (e ErrAlreadySent) Error() string { return fmt.Sprintf("draft %s already sent", e.DraftID) }

// ---------------------------------------------------------------------------
// CardStore — PostgreSQL storage for decision cards
// ---------------------------------------------------------------------------

// CardStore handles persistence for DecisionCard entities.
type CardStore struct {
	db *sqlx.DB
}

// NewCardStore creates a new CardStore.
func NewCardStore(db *sqlx.DB) *CardStore { return &CardStore{db: db} }

// GetCard retrieves a card by ID, verifying it exists.
func (s *CardStore) GetCard(ctx context.Context, cardID uuid.UUID) (*models.DecisionCard, error) {
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
			return nil, ErrCardNotFound{CardID: cardID}
		}
		return nil, fmt.Errorf("get card: %w", err)
	}
	return &card, nil
}

// GetCardOwnedBy retrieves a card and verifies ownership in one query.
func (s *CardStore) GetCardOwnedBy(ctx context.Context, cardID, userID uuid.UUID) (*models.DecisionCard, error) {
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
			return nil, ErrCardOwnership{CardID: cardID, UserID: userID}
		}
		return nil, fmt.Errorf("get card owned by: %w", err)
	}
	return &card, nil
}

// UpdateCardState atomically updates the card state and bumps server_version.
func (s *CardStore) UpdateCardState(ctx context.Context, cardID uuid.UUID, newState string) error {
	query := `
		UPDATE decision_cards
		SET card_state = $1, server_version = server_version + 1, updated_at = NOW()
		WHERE id = $2
	`
	res, err := s.db.ExecContext(ctx, query, newState, cardID)
	if err != nil {
		return fmt.Errorf("update card state: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCardNotFound{CardID: cardID}
	}
	return nil
}

// UpdateCardStateTx updates card state inside a transaction.
func (s *CardStore) UpdateCardStateTx(ctx context.Context, tx *sqlx.Tx, cardID uuid.UUID, newState string) error {
	query := `
		UPDATE decision_cards
		SET card_state = $1, server_version = server_version + 1, updated_at = NOW()
		WHERE id = $2
	`
	res, err := tx.ExecContext(ctx, query, newState, cardID)
	if err != nil {
		return fmt.Errorf("update card state tx: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCardNotFound{CardID: cardID}
	}
	return nil
}

// MarkCardSent marks a card as sent with timestamp.
func (s *CardStore) MarkCardSent(ctx context.Context, cardID uuid.UUID) error {
	query := `
		UPDATE decision_cards
		SET card_state = 'sent', sent_at = NOW(), server_version = server_version + 1, updated_at = NOW()
		WHERE id = $1
	`
	res, err := s.db.ExecContext(ctx, query, cardID)
	if err != nil {
		return fmt.Errorf("mark card sent: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCardNotFound{CardID: cardID}
	}
	return nil
}

// MarkCardSentTx marks a card as sent inside a transaction.
func (s *CardStore) MarkCardSentTx(ctx context.Context, tx *sqlx.Tx, cardID uuid.UUID) error {
	query := `
		UPDATE decision_cards
		SET card_state = 'sent', sent_at = NOW(), server_version = server_version + 1, updated_at = NOW()
		WHERE id = $1
	`
	res, err := tx.ExecContext(ctx, query, cardID)
	if err != nil {
		return fmt.Errorf("mark card sent tx: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrCardNotFound{CardID: cardID}
	}
	return nil
}

// GetCardCitations returns the chunk citations for a card's sources.
func (s *CardStore) GetCardCitations(ctx context.Context, cardID, userID uuid.UUID) ([]models.ChunkCitation, error) {
	query := `
		SELECT chunk_citations
		FROM decision_cards
		WHERE id = $1 AND user_id = $2
	`
	var raw json.RawMessage
	if err := s.db.GetContext(ctx, &raw, query, cardID, userID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrCardOwnership{CardID: cardID, UserID: userID}
		}
		return nil, fmt.Errorf("get card citations: %w", err)
	}
	var citations []models.ChunkCitation
	if len(raw) == 0 || string(raw) == "null" {
		return []models.ChunkCitation{}, nil
	}
	if err := json.Unmarshal(raw, &citations); err != nil {
		return nil, fmt.Errorf("unmarshal citations: %w", err)
	}
	return citations, nil
}

// ---------------------------------------------------------------------------
// DraftStore — PostgreSQL storage for email drafts
// ---------------------------------------------------------------------------

// DraftStore handles persistence for Draft entities.
type DraftStore struct {
	db *sqlx.DB
}

// NewDraftStore creates a new DraftStore.
func NewDraftStore(db *sqlx.DB) *DraftStore { return &DraftStore{db: db} }

// CreateDraft inserts a new draft into the database.
func (s *DraftStore) CreateDraft(ctx context.Context, draft *models.Draft) error {
	query := `
		INSERT INTO drafts (
			id, card_id, user_id, thread_id, draft_body, subject_line,
			tone_profile, in_reply_to, references, model_used, tokens_used,
			user_approved, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)
	`
	_, err := s.db.ExecContext(ctx, query,
		draft.ID, draft.CardID, draft.UserID, draft.ThreadID,
		draft.DraftBody, draft.SubjectLine, draft.ToneProfile,
		draft.InReplyTo, pqStringArray(draft.References),
		draft.ModelUsed, draft.TokensUsed,
		draft.UserApproved, draft.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create draft: %w", err)
	}
	return nil
}

// GetDraft retrieves a draft by ID.
func (s *DraftStore) GetDraft(ctx context.Context, draftID uuid.UUID) (*models.Draft, error) {
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
			return nil, ErrDraftNotFound{DraftID: draftID}
		}
		return nil, fmt.Errorf("get draft: %w", err)
	}
	return &draft, nil
}

// GetDraftOwnedBy retrieves a draft and verifies ownership.
func (s *DraftStore) GetDraftOwnedBy(ctx context.Context, draftID, userID uuid.UUID) (*models.Draft, error) {
	var draft models.Draft
	query := `
		SELECT id, card_id, user_id, thread_id, draft_body, subject_line,
			   tone_profile, in_reply_to, references, model_used, tokens_used,
			   user_approved, sent_at, created_at
		FROM drafts
		WHERE id = $1 AND user_id = $2
	`
	if err := s.db.GetContext(ctx, &draft, query, draftID, userID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrDraftNotFound{DraftID: draftID}
		}
		return nil, fmt.Errorf("get draft owned by: %w", err)
	}
	return &draft, nil
}

// UpdateDraftBody updates the draft body and marks as user-edited.
func (s *DraftStore) UpdateDraftBody(ctx context.Context, draftID uuid.UUID, userID uuid.UUID, body string) error {
	query := `
		UPDATE drafts
		SET draft_body = $1, updated_at = NOW()
		WHERE id = $2 AND user_id = $3
	`
	res, err := s.db.ExecContext(ctx, query, body, draftID, userID)
	if err != nil {
		return fmt.Errorf("update draft body: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrDraftNotFound{DraftID: draftID}
	}
	return nil
}

// ApproveDraftTx marks a draft as approved inside a transaction.
func (s *DraftStore) ApproveDraftTx(ctx context.Context, tx *sqlx.Tx, draftID uuid.UUID, userID uuid.UUID) error {
	// First verify ownership and current state
	var current models.Draft
	checkQuery := `SELECT user_approved, sent_at FROM drafts WHERE id = $1 AND user_id = $2 FOR UPDATE`
	if err := tx.GetContext(ctx, &current, checkQuery, draftID, userID); err != nil {
		if err == sql.ErrNoRows {
			return ErrDraftNotFound{DraftID: draftID}
		}
		return fmt.Errorf("approve draft lock: %w", err)
	}
	if current.UserApproved {
		return ErrAlreadyApproved{DraftID: draftID}
	}
	if current.SentAt != nil {
		return ErrAlreadySent{DraftID: draftID}
	}

	query := `UPDATE drafts SET user_approved = true, updated_at = NOW() WHERE id = $1`
	if _, err := tx.ExecContext(ctx, query, draftID); err != nil {
		return fmt.Errorf("approve draft: %w", err)
	}
	return nil
}

// MarkDraftSentTx marks a draft as sent inside a transaction.
func (s *DraftStore) MarkDraftSentTx(ctx context.Context, tx *sqlx.Tx, draftID uuid.UUID, messageID string) error {
	query := `
		UPDATE drafts
		SET sent_at = NOW(), message_id = $2, updated_at = NOW()
		WHERE id = $1
	`
	res, err := tx.ExecContext(ctx, query, draftID, messageID)
	if err != nil {
		return fmt.Errorf("mark draft sent tx: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrDraftNotFound{DraftID: draftID}
	}
	return nil
}

// LogDecision records a decision to the audit log.
func (s *DraftStore) LogDecision(ctx context.Context, userID, cardID uuid.UUID, decisionType, details string) error {
	query := `
		INSERT INTO decision_logs (id, user_id, card_id, decision_type, details, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := s.db.ExecContext(ctx, query, uuid.New(), userID, cardID, decisionType, details, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("log decision: %w", err)
	}
	return nil
}

// LogDecisionTx records a decision inside a transaction.
func (s *DraftStore) LogDecisionTx(ctx context.Context, tx *sqlx.Tx, userID, cardID uuid.UUID, decisionType, details string) error {
	query := `
		INSERT INTO decision_logs (id, user_id, card_id, decision_type, details, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := tx.ExecContext(ctx, query, uuid.New(), userID, cardID, decisionType, details, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("log decision tx: %w", err)
	}
	return nil
}

// GetLatestDraftForCard returns the most recent draft for a card.
func (s *DraftStore) GetLatestDraftForCard(ctx context.Context, cardID uuid.UUID) (*models.Draft, error) {
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
			return nil, ErrDraftNotFound{}
		}
		return nil, fmt.Errorf("get latest draft: %w", err)
	}
	return &draft, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// pqStringArray converts a []string to a PostgreSQL-compatible string array.
// For sqlx with pq driver, []string is handled automatically via pq.Array,
// but we use a simple helper for explicitness.
func pqStringArray(sa []string) interface{} {
	if sa == nil {
		return pqStringSlice{}
	}
	return pqStringSlice(sa)
}

// pqStringSlice is a wrapper for PostgreSQL text arrays.
type pqStringSlice []string
