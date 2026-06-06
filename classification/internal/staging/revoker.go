package staging

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Revoker
// ---------------------------------------------------------------------------

// Revoker handles user-initiated revocation of active auto-handle rules.
// Invariants:
//   - Revocation is user-initiated only (not automatic).
//   - Once revoked, future matching emails route to Decision Stack.
//   - No retroactive undo: emails already handled stay handled.
//   - Revocation is terminal: a revoked rule cannot be re-activated.
type Revoker struct {
	db       *sql.DB
	notifier *Notifier
	log      *slog.Logger
}

// NewRevoker creates a Revoker.
func NewRevoker(db *sql.DB, notifier *Notifier, log *slog.Logger) *Revoker {
	return &Revoker{
		db:       db,
		notifier: notifier,
		log:      log.With("component", "revoker"),
	}
}

// Revoke turns off an active auto-handle rule.
// Steps:
//  1. UPDATE auto_handle_rules SET status='revoked', revoked_at=NOW() WHERE id=$1 AND status='active'
//  2. Notify user of revocation
//  3. Log revocation with full context
func (r *Revoker) Revoke(ctx context.Context, ruleID uuid.UUID) error {
	logger := r.log.With("rule_id", ruleID)

	// -----------------------------------------------------------------------
	// 1. Atomic UPDATE — only revoke if currently active.
	// -----------------------------------------------------------------------
	result, err := r.db.ExecContext(ctx, `
		UPDATE auto_handle_rules
		SET status = 'revoked',
		    revoked_at = NOW(),
		    updated_at = NOW()
		WHERE id = $1
		  AND status = 'active'
	`, ruleID)
	if err != nil {
		logger.Error("database error during revocation", "error", err)
		return fmt.Errorf("update rule status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		logger.Error("failed to get rows affected", "error", err)
		return fmt.Errorf("rows affected: %w", err)
	}

	if rows == 0 {
		// Rule was not active — check current status for better error message.
		var currentStatus string
		var ruleName string
		var userID uuid.UUID
		err := r.db.QueryRowContext(ctx, `
			SELECT status, name, user_id FROM auto_handle_rules WHERE id = $1
		`, ruleID).Scan(&currentStatus, &ruleName, &userID)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("rule %s not found", ruleID)
			}
			return fmt.Errorf("query rule status: %w", err)
		}

		if currentStatus == "revoked" {
			return fmt.Errorf("rule %s is already revoked", ruleID)
		}
		if currentStatus == "staged" {
			return fmt.Errorf("rule %s is still staged — cannot revoke a staged rule (use cancel instead)", ruleID)
		}
		return fmt.Errorf("rule %s has unexpected status '%s' for revocation", ruleID, currentStatus)
	}

	// Fetch rule details for notification.
	var ruleName string
	var userID uuid.UUID
	if err := r.db.QueryRowContext(ctx, `
		SELECT name, user_id FROM auto_handle_rules WHERE id = $1
	`, ruleID).Scan(&ruleName, &userID); err != nil {
		logger.Warn("failed to fetch rule details for notification", "error", err)
		ruleName = "Unknown"
		userID = uuid.Nil
	}

	logger.Info("rule revoked",
		"rule_name", ruleName,
		"user_id", userID,
		"revoked_at", time.Now().UTC(),
	)

	// -----------------------------------------------------------------------
	// 2. Notify user that the rule has been turned off.
	// -----------------------------------------------------------------------
	if userID != uuid.Nil {
		if err := r.notifier.NotifyRevoked(ctx, userID, ruleName); err != nil {
			// Notification failure is non-fatal — the rule IS revoked.
			logger.Warn("revocation notification failed", "error", err)
		}
	}

	// -----------------------------------------------------------------------
	// 3. Log revocation with full context for audit trail.
	// -----------------------------------------------------------------------
	logger.Info("revocation complete",
		"rule_id", ruleID,
		"rule_name", ruleName,
		"user_id", userID,
		"effect", "future matching emails will route to Decision Stack",
		"retroactive_undo", false,
	)

	return nil
}

// GetRevokedRules returns all revoked rules for a user (for audit / UI).
func (r *Revoker) GetRevokedRules(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id FROM auto_handle_rules WHERE user_id = $1 AND status = 'revoked' ORDER BY revoked_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("query revoked rules: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			r.log.Error("scan revoked rule id", "error", err)
			continue
		}
		ids = append(ids, id)
	}

	return ids, rows.Err()
}

// IsRevoked checks whether a rule is currently revoked.
func (r *Revoker) IsRevoked(ctx context.Context, ruleID uuid.UUID) (bool, error) {
	var status string
	err := r.db.QueryRowContext(ctx, `
		SELECT status FROM auto_handle_rules WHERE id = $1
	`, ruleID).Scan(&status)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, fmt.Errorf("rule %s not found", ruleID)
		}
		return false, fmt.Errorf("query rule status: %w", err)
	}
	return status == "revoked", nil
}
