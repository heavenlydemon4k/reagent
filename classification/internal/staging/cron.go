package staging

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/decisionstack/classification/internal/models"
)

const (
	defaultInterval = 15 * time.Minute
	stagingWindow   = 48 * time.Hour
)

// ---------------------------------------------------------------------------
// StagingCron
// ---------------------------------------------------------------------------

// StagingCron periodically scans for staged rules that have passed the 48-hour
// trust-building window and activates them.
type StagingCron struct {
	db        *sql.DB
	activator *Activator
	interval  time.Duration
	log       *slog.Logger

	mu      sync.RWMutex
	running bool
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// NewStagingCron creates a StagingCron.
// interval defaults to 15 minutes if zero.
func NewStagingCron(db *sql.DB, activator *Activator, interval time.Duration, log *slog.Logger) *StagingCron {
	if interval <= 0 {
		interval = defaultInterval
	}
	if log == nil {
		log = slog.Default()
	}
	return &StagingCron{
		db:        db,
		activator: activator,
		interval:  interval,
		log:       log.With("component", "staging_cron"),
		stopCh:    make(chan struct{}),
	}
}

// Start begins the cron ticker. Blocks until the context is cancelled or Stop() is called.
func (c *StagingCron) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return fmt.Errorf("staging cron already running")
	}
	c.running = true
	c.mu.Unlock()

	c.log.Info("starting staging cron", "interval", c.interval, "staging_window", stagingWindow)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Run immediately on start, then on each tick.
	if err := c.tick(ctx); err != nil {
		c.log.Error("initial tick failed", "error", err)
	}

	for {
		select {
		case <-ticker.C:
			if err := c.tick(ctx); err != nil {
				c.log.Error("tick failed", "error", err)
			}
		case <-ctx.Done():
			c.log.Info("context cancelled, stopping staging cron")
			c.gracefulStop()
			return ctx.Err()
		case <-c.stopCh:
			c.log.Info("stop signal received, stopping staging cron")
			c.gracefulStop()
			return nil
		}
	}
}

// Stop signals the cron to shut down gracefully.
func (c *StagingCron) Stop() {
	c.mu.RLock()
	if !c.running {
		c.mu.RUnlock()
		return
	}
	c.mu.RUnlock()
	close(c.stopCh)
}

// IsRunning reports whether the cron is active.
func (c *StagingCron) IsRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running
}

// ---------------------------------------------------------------------------
// Tick — core logic
// ---------------------------------------------------------------------------

// tick queries for expired staged rules and activates them.
func (c *StagingCron) tick(ctx context.Context) error {
	c.wg.Add(1)
	defer c.wg.Done()

	start := time.Now()
	c.log.Info("tick started", "tick_at", start.UTC())

	// Query for staged rules where staged_at < NOW() - 48 hours.
	rows, err := c.db.QueryContext(ctx, `
		SELECT id, user_id, name, predicate, action_type, action_config,
		       confidence_threshold, status, staged_at, activated_at,
		       revoked_at, usage_count, created_at
		FROM auto_handle_rules
		WHERE status = 'staged'
		  AND staged_at < NOW() - INTERVAL '48 hours'
		ORDER BY staged_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 100
	`)
	if err != nil {
		return fmt.Errorf("query expired staged rules: %w", err)
	}
	defer rows.Close()

	var rules []models.AutoHandleRule
	for rows.Next() {
		var rule models.AutoHandleRule
		var predicateJSON []byte
		if err := rows.Scan(
			&rule.ID, &rule.UserID, &rule.Name, &predicateJSON, &rule.ActionType,
			&rule.ActionConfig, &rule.ConfidenceThreshold, &rule.Status,
			&rule.StagedAt, &rule.ActivatedAt, &rule.RevokedAt,
			&rule.UsageCount, &rule.CreatedAt,
		); err != nil {
			c.log.Error("scan staged rule", "error", err)
			continue
		}
		// Deserialize predicate JSON.
		if len(predicateJSON) > 0 {
			if err := json.Unmarshal(predicateJSON, &rule.Predicate); err != nil {
				c.log.Error("unmarshal predicate", "rule_id", rule.ID, "error", err)
				continue
			}
		}
		rules = append(rules, rule)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate staged rules: %w", err)
	}

	if len(rules) == 0 {
		c.log.Debug("no expired staged rules found")
		return nil
	}

	c.log.Info("found expired staged rules", "count", len(rules))

	// Activate each rule.
	activated, failed := c.activator.BulkActivate(ctx, rules)
	c.log.Info("tick completed",
		"activated", activated,
		"failed", failed,
		"duration_ms", time.Since(start).Milliseconds(),
	)

	return nil
}

// ---------------------------------------------------------------------------
// Graceful shutdown
// ---------------------------------------------------------------------------

func (c *StagingCron) gracefulStop() {
	c.log.Info("waiting for current tick to complete")
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		c.log.Info("tick completed, cron stopped")
	case <-time.After(30 * time.Second):
		c.log.Warn("shutdown timeout: cron stopped with tick in progress")
	}

	c.mu.Lock()
	c.running = false
	c.mu.Unlock()
}


