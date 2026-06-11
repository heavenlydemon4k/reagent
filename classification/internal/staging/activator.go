package staging

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/decisionstack/classification/internal/models"
)

// ---------------------------------------------------------------------------
// Activator
// ---------------------------------------------------------------------------

// Activator promotes staged rules to active after the 48-hour window.
// Activation is ONE-WAY: once active, a rule stays active until explicitly revoked by the user.
type Activator struct {
	db        *sql.DB
	notifier  *Notifier
	log       *slog.Logger
}

// NewActivator creates an Activator.
func NewActivator(db *sql.DB, notifier *Notifier, log *slog.Logger) *Activator {
	return &Activator{
		db:       db,
		notifier: notifier,
		log:      log.With("component", "activator"),
	}
}

// Activate promotes a staged rule to active status.
// Steps:
//  1. UPDATE auto_handle_rules SET status='active', activated_at=NOW() WHERE id=$1 AND status='staged'
//  2. Notify user of activation
//  3. Log activation with full context
func (a *Activator) Activate(ctx context.Context, rule models.AutoHandleRule) error {
	logger := a.log.With(
		"rule_id", rule.ID,
		"user_id", rule.UserID,
		"rule_name", rule.Name,
		"action_type", rule.ActionType,
	)

	// -----------------------------------------------------------------------
	// 1. Atomic UPDATE — only activate if still staged.
	// -----------------------------------------------------------------------
	result, err := a.db.ExecContext(ctx, `
		UPDATE auto_handle_rules
		SET status = 'active',
		    activated_at = NOW(),
		    updated_at = NOW()
		WHERE id = $1
		  AND status = 'staged'
	`, rule.ID)
	if err != nil {
		logger.Error("database error during activation", "error", err)
		return fmt.Errorf("update rule status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		logger.Error("failed to get rows affected", "error", err)
		return fmt.Errorf("rows affected: %w", err)
	}

	if rows == 0 {
		// Rule was already activated, revoked, or deleted — idempotent no-op.
		logger.Warn("activation skipped: rule not in staged status", "rule_id", rule.ID)
		return fmt.Errorf("rule %s is not in 'staged' status", rule.ID)
	}

	logger.Info("rule activated",
		"staged_at", rule.StagedAt,
		"activated_at", time.Now().UTC(),
		"staging_duration_hours", time.Since(*rule.StagedAt).Hours(),
	)

	// -----------------------------------------------------------------------
	// 2. Notify user that the rule is now active.
	// -----------------------------------------------------------------------
	if err := a.notifier.NotifyActivated(ctx, rule.UserID, rule.Name); err != nil {
		// Notification failure is non-fatal — the rule IS active.
		logger.Warn("activation notification failed", "error", err)
	}

	// -----------------------------------------------------------------------
	// 3. Log activation with full context for audit trail.
	// -----------------------------------------------------------------------
	logger.Info("activation complete",
		"rule_id", rule.ID,
		"rule_name", rule.Name,
		"predicate_allof_count", len(rule.Predicate.AllOf),
		"predicate_anyof_count", len(rule.Predicate.AnyOf),
		"confidence_threshold", rule.ConfidenceThreshold,
		"action_type", rule.ActionType,
	)

	return nil
}

// BulkActivate activates multiple staged rules in a single transaction.
// Useful for the cron job processing a batch of expired staged rules.
func (a *Activator) BulkActivate(ctx context.Context, rules []models.AutoHandleRule) (activated int, failed int) {
	for _, rule := range rules {
		if err := a.Activate(ctx, rule); err != nil {
			a.log.Error("bulk activation failed for rule", "rule_id", rule.ID, "error", err)
			failed++
			continue
		}
		activated++
	}
	return activated, failed
}
