package auto

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/decisionstack/classification/internal/models"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const (
	createRuleSQL = `
		INSERT INTO auto_handle_rules (
			id, user_id, name, predicate, action_type, action_config,
			confidence_threshold, status, staged_at, activated_at,
			revoked_at, usage_count, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`

	getRuleByIDSQL = `
		SELECT id, user_id, name, predicate, action_type, action_config,
			   confidence_threshold, status, staged_at, activated_at,
			   revoked_at, usage_count, created_at
		FROM auto_handle_rules
		WHERE id = $1
	`

	getRulesByUserSQL = `
		SELECT id, user_id, name, predicate, action_type, action_config,
			   confidence_threshold, status, staged_at, activated_at,
			   revoked_at, usage_count, created_at
		FROM auto_handle_rules
		WHERE user_id = $1
		ORDER BY usage_count DESC, created_at DESC
	`

	getActiveRulesByUserSQL = `
		SELECT id, user_id, name, predicate, action_type, action_config,
			   confidence_threshold, status, staged_at, activated_at,
			   revoked_at, usage_count, created_at
		FROM auto_handle_rules
		WHERE user_id = $1 AND status = 'active'
		ORDER BY usage_count DESC, created_at DESC
	`

	updateRuleSQL = `
		UPDATE auto_handle_rules SET
			name = $2,
			predicate = $3,
			action_type = $4,
			action_config = $5,
			confidence_threshold = $6,
			updated_at = $7
		WHERE id = $1
	`

	updateStatusSQL = `
		UPDATE auto_handle_rules SET
			status = $2,
			staged_at = $3,
			activated_at = $4,
			revoked_at = $5,
			updated_at = $6
		WHERE id = $1
	`

	deleteRuleSQL = `
		DELETE FROM auto_handle_rules WHERE id = $1
	`

	incrementUsageSQL = `
		UPDATE auto_handle_rules
		SET usage_count = usage_count + 1,
			last_used_at = $2
		WHERE id = $1
	`
)

// RuleStore provides CRUD operations for auto_handle_rules in PostgreSQL.
type RuleStore struct {
	db *sql.DB
}

// NewRuleStore creates a new RuleStore backed by PostgreSQL.
func NewRuleStore(db *sql.DB) *RuleStore {
	return &RuleStore{db: db}
}

// Create inserts a new auto-handle rule into the database.
func (s *RuleStore) Create(ctx context.Context, rule *models.AutoHandleRule) error {
	if rule.ID == uuid.Nil {
		rule.ID = uuid.Must(uuid.NewRandom())
	}
	if rule.Status == "" {
		rule.Status = "staged"
	}
	if rule.ConfidenceThreshold == 0 {
		rule.ConfidenceThreshold = 0.92
	}
	rule.CreatedAt = time.Now().UTC()

	predicateJSON, err := json.Marshal(rule.Predicate)
	if err != nil {
		return fmt.Errorf("marshal predicate: %w", err)
	}

	_, err = s.db.ExecContext(ctx, createRuleSQL,
		rule.ID,
		rule.UserID,
		rule.Name,
		predicateJSON,
		rule.ActionType,
		rule.ActionConfig,
		rule.ConfidenceThreshold,
		rule.Status,
		rule.StagedAt,
		rule.ActivatedAt,
		rule.RevokedAt,
		rule.UsageCount,
		rule.CreatedAt,
	)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return fmt.Errorf("rule with name %q already exists for user: %w", rule.Name, models.ClassificationError{
				Code:    "duplicate_rule",
				Message: fmt.Sprintf("rule %q already exists", rule.Name),
				Retry:   false,
			})
		}
		return fmt.Errorf("insert rule: %w", err)
	}

	return nil
}

// GetByID retrieves a single rule by its UUID.
func (s *RuleStore) GetByID(ctx context.Context, id uuid.UUID) (*models.AutoHandleRule, error) {
	var rule models.AutoHandleRule
	var predicateJSON []byte

	err := s.db.QueryRowContext(ctx, getRuleByIDSQL, id).Scan(
		&rule.ID,
		&rule.UserID,
		&rule.Name,
		&predicateJSON,
		&rule.ActionType,
		&rule.ActionConfig,
		&rule.ConfidenceThreshold,
		&rule.Status,
		&rule.StagedAt,
		&rule.ActivatedAt,
		&rule.RevokedAt,
		&rule.UsageCount,
		&rule.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ClassificationError{
				Code:    models.ErrCodeRuleNotFound,
				Message: fmt.Sprintf("rule %s not found", id),
				Retry:   false,
			}
		}
		return nil, fmt.Errorf("query rule by id: %w", err)
	}

	if err := json.Unmarshal(predicateJSON, &rule.Predicate); err != nil {
		return nil, fmt.Errorf("unmarshal predicate: %w", err)
	}

	return &rule, nil
}

// GetByUser retrieves all rules for a user, ordered by usage_count DESC.
func (s *RuleStore) GetByUser(ctx context.Context, userID uuid.UUID) ([]models.AutoHandleRule, error) {
	rows, err := s.db.QueryContext(ctx, getRulesByUserSQL, userID)
	if err != nil {
		return nil, fmt.Errorf("query rules by user: %w", err)
	}
	defer rows.Close()

	return scanRules(rows)
}

// GetActiveRules retrieves active rules for a user, ordered by usage_count DESC.
func (s *RuleStore) GetActiveRules(ctx context.Context, userID uuid.UUID) ([]models.AutoHandleRule, error) {
	rows, err := s.db.QueryContext(ctx, getActiveRulesByUserSQL, userID)
	if err != nil {
		return nil, fmt.Errorf("query active rules: %w", err)
	}
	defer rows.Close()

	return scanRules(rows)
}

// Update modifies an existing rule's definition (not status).
func (s *RuleStore) Update(ctx context.Context, rule *models.AutoHandleRule) error {
	predicateJSON, err := json.Marshal(rule.Predicate)
	if err != nil {
		return fmt.Errorf("marshal predicate: %w", err)
	}

	result, err := s.db.ExecContext(ctx, updateRuleSQL,
		rule.ID,
		rule.Name,
		predicateJSON,
		rule.ActionType,
		rule.ActionConfig,
		rule.ConfidenceThreshold,
		time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("update rule: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return models.ClassificationError{
			Code:    models.ErrCodeRuleNotFound,
			Message: fmt.Sprintf("rule %s not found for update", rule.ID),
			Retry:   false,
		}
	}

	return nil
}

// UpdateStatus transitions a rule between staged/active/revoked states.
func (s *RuleStore) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	now := time.Now().UTC()

	var stagedAt, activatedAt, revokedAt *time.Time
	switch status {
	case "staged":
		stagedAt = &now
	case "active":
		activatedAt = &now
	case "revoked":
		revokedAt = &now
	default:
		return fmt.Errorf("invalid status %q: must be staged, active, or revoked", status)
	}

	result, err := s.db.ExecContext(ctx, updateStatusSQL,
		id,
		status,
		stagedAt,
		activatedAt,
		revokedAt,
		now,
	)
	if err != nil {
		return fmt.Errorf("update rule status: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return models.ClassificationError{
			Code:    models.ErrCodeRuleNotFound,
			Message: fmt.Sprintf("rule %s not found for status update", id),
			Retry:   false,
		}
	}

	return nil
}

// Delete permanently removes a rule from the database.
func (s *RuleStore) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := s.db.ExecContext(ctx, deleteRuleSQL, id)
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return models.ClassificationError{
			Code:    models.ErrCodeRuleNotFound,
			Message: fmt.Sprintf("rule %s not found for deletion", id),
			Retry:   false,
		}
	}

	return nil
}

// IncrementUsage bumps the usage_count and last_used_at for a rule.
func (s *RuleStore) IncrementUsage(ctx context.Context, ruleID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, incrementUsageSQL, ruleID, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("increment usage: %w", err)
	}
	return nil
}

// scanRules reads multiple rule rows from a sql.Rows result set.
func scanRules(rows *sql.Rows) ([]models.AutoHandleRule, error) {
	var rules []models.AutoHandleRule

	for rows.Next() {
		var rule models.AutoHandleRule
		var predicateJSON []byte

		err := rows.Scan(
			&rule.ID,
			&rule.UserID,
			&rule.Name,
			&predicateJSON,
			&rule.ActionType,
			&rule.ActionConfig,
			&rule.ConfidenceThreshold,
			&rule.Status,
			&rule.StagedAt,
			&rule.ActivatedAt,
			&rule.RevokedAt,
			&rule.UsageCount,
			&rule.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan rule: %w", err)
		}

		if err := json.Unmarshal(predicateJSON, &rule.Predicate); err != nil {
			return nil, fmt.Errorf("unmarshal predicate: %w", err)
		}

		rules = append(rules, rule)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rules: %w", err)
	}

	return rules, nil
}
