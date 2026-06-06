// Package rules provides persistence and retrieval of auto-handle rules.
package rules

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/decisionstack/classification/internal/db"
	"github.com/decisionstack/classification/internal/models"
	"github.com/google/uuid"
)

// Store abstracts rule persistence.
type Store struct {
	pool *db.Pool
}

// NewStore creates a rule store backed by PostgreSQL.
func NewStore(pool *db.Pool) *Store {
	return &Store{pool: pool}
}

// ListActive returns all active rules for a user.
func (s *Store) ListActive(ctx context.Context, userID uuid.UUID) ([]models.AutoHandleRule, error) {
	query := `
		SELECT id, user_id, name, predicate, action_type, action_config,
		       confidence_threshold, status, staged_at, activated_at, revoked_at,
		       usage_count, created_at
		FROM auto_handle_rules
		WHERE user_id = $1 AND status = 'active'
		ORDER BY created_at DESC
	`
	rows, err := s.pool.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("query active rules: %w", err)
	}
	defer rows.Close()

	return scanRules(rows)
}

// GetByID fetches a single rule by its ID.
func (s *Store) GetByID(ctx context.Context, ruleID uuid.UUID) (*models.AutoHandleRule, error) {
	query := `
		SELECT id, user_id, name, predicate, action_type, action_config,
		       confidence_threshold, status, staged_at, activated_at, revoked_at,
		       usage_count, created_at
		FROM auto_handle_rules
		WHERE id = $1
	`
	rule := &models.AutoHandleRule{}
	var predJSON []byte
	err := s.pool.QueryRowContext(ctx, query, ruleID).Scan(
		&rule.ID, &rule.UserID, &rule.Name, &predJSON, &rule.ActionType, &rule.ActionConfig,
		&rule.ConfidenceThreshold, &rule.Status, &rule.StagedAt, &rule.ActivatedAt, &rule.RevokedAt,
		&rule.UsageCount, &rule.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("rule not found: %s", ruleID)
	}
	if err != nil {
		return nil, fmt.Errorf("query rule: %w", err)
	}
	if err := json.Unmarshal(predJSON, &rule.Predicate); err != nil {
		return nil, fmt.Errorf("unmarshal predicate: %w", err)
	}
	return rule, nil
}

// ListByUser returns paginated rules for a user with optional status filter.
func (s *Store) ListByUser(ctx context.Context, userID uuid.UUID, status string, limit, offset int) ([]models.AutoHandleRule, error) {
	var query string
	var args []interface{}

	if status != "" {
		query = `
			SELECT id, user_id, name, predicate, action_type, action_config,
			       confidence_threshold, status, staged_at, activated_at, revoked_at,
			       usage_count, created_at
			FROM auto_handle_rules
			WHERE user_id = $1 AND status = $2
			ORDER BY created_at DESC
			LIMIT $3 OFFSET $4
		`
		args = []interface{}{userID, status, limit, offset}
	} else {
		query = `
			SELECT id, user_id, name, predicate, action_type, action_config,
			       confidence_threshold, status, staged_at, activated_at, revoked_at,
			       usage_count, created_at
			FROM auto_handle_rules
			WHERE user_id = $1
			ORDER BY created_at DESC
			LIMIT $2 OFFSET $3
		`
		args = []interface{}{userID, limit, offset}
	}

	rows, err := s.pool.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query rules: %w", err)
	}
	defer rows.Close()

	return scanRules(rows)
}

// Create inserts a new rule into staging.
func (s *Store) Create(ctx context.Context, userID uuid.UUID, name string, predicate models.RulePredicate, actionType string, actionConfig json.RawMessage, confidenceThreshold float64) (*models.AutoHandleRule, error) {
	now := time.Now().UTC()
	stagedAt := now

	predJSON, err := json.Marshal(predicate)
	if err != nil {
		return nil, fmt.Errorf("marshal predicate: %w", err)
	}

	rule := &models.AutoHandleRule{
		ID:                  uuid.New(),
		UserID:              userID,
		Name:                name,
		Predicate:           predicate,
		ActionType:          actionType,
		ActionConfig:        actionConfig,
		ConfidenceThreshold: confidenceThreshold,
		Status:              "staged",
		StagedAt:            &stagedAt,
		UsageCount:          0,
		CreatedAt:           now,
	}

	query := `
		INSERT INTO auto_handle_rules
		(id, user_id, name, predicate, action_type, action_config,
		 confidence_threshold, status, staged_at, usage_count, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	_, err = s.pool.ExecContext(ctx, query,
		rule.ID, rule.UserID, rule.Name, predJSON, rule.ActionType, rule.ActionConfig,
		rule.ConfidenceThreshold, rule.Status, rule.StagedAt, rule.UsageCount, rule.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert rule: %w", err)
	}

	return rule, nil
}

// Activate promotes a staged rule to active.
func (s *Store) Activate(ctx context.Context, ruleID uuid.UUID) error {
	now := time.Now().UTC()
	query := `
		UPDATE auto_handle_rules
		SET status = 'active', activated_at = $1
		WHERE id = $2 AND status = 'staged'
	`
	res, err := s.pool.ExecContext(ctx, query, now, ruleID)
	if err != nil {
		return fmt.Errorf("activate rule: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("rule not found or not in staged status: %s", ruleID)
	}
	return nil
}

// Revoke marks an active or staged rule as revoked.
func (s *Store) Revoke(ctx context.Context, ruleID uuid.UUID) error {
	now := time.Now().UTC()
	query := `
		UPDATE auto_handle_rules
		SET status = 'revoked', revoked_at = $1
		WHERE id = $2 AND status IN ('staged', 'active')
	`
	res, err := s.pool.ExecContext(ctx, query, now, ruleID)
	if err != nil {
		return fmt.Errorf("revoke rule: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("rule not found or already revoked: %s", ruleID)
	}
	return nil
}

// IncrementUsage bumps the usage_count for a rule.
func (s *Store) IncrementUsage(ctx context.Context, ruleID uuid.UUID) error {
	query := `UPDATE auto_handle_rules SET usage_count = usage_count + 1 WHERE id = $1`
	_, err := s.pool.ExecContext(ctx, query, ruleID)
	if err != nil {
		return fmt.Errorf("increment usage: %w", err)
	}
	return nil
}

// scanRules scans a sql.Rows into a slice of AutoHandleRule.
func scanRules(rows *sql.Rows) ([]models.AutoHandleRule, error) {
	var rules []models.AutoHandleRule
	for rows.Next() {
		var rule models.AutoHandleRule
		var predJSON []byte
		err := rows.Scan(
			&rule.ID, &rule.UserID, &rule.Name, &predJSON, &rule.ActionType, &rule.ActionConfig,
			&rule.ConfidenceThreshold, &rule.Status, &rule.StagedAt, &rule.ActivatedAt, &rule.RevokedAt,
			&rule.UsageCount, &rule.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan rule: %w", err)
		}
		if err := json.Unmarshal(predJSON, &rule.Predicate); err != nil {
			return nil, fmt.Errorf("unmarshal predicate: %w", err)
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}
	return rules, nil
}
