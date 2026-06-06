package poll

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Scheduler periodically queries the database for active email accounts and
// submits FetchJobs to the worker pool. It respects account-specific poll
// intervals — new accounts are polled more frequently.
type Scheduler struct {
	db        *sql.DB
	pool      *WorkerPool
	interval  time.Duration // default tick interval
	log       *slog.Logger

	// mu protects the running flag and stop channel.
	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// NewScheduler creates a new Scheduler.
func NewScheduler(db *sql.DB, pool *WorkerPool, interval time.Duration, log *slog.Logger) *Scheduler {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	return &Scheduler{
		db:       db,
		pool:     pool,
		interval: interval,
		log:      log.With("component", "scheduler"),
		stopCh:   make(chan struct{}),
	}
}

// AccountRow represents a single row from the email_accounts query.
type AccountRow struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	Provider      string // "gmail" | "outlook"
	PollInterval  time.Duration
	IsActive      bool
	CreatedAt     time.Time
	LastPolledAt  *time.Time
}

// Start begins the scheduler tick loop. On each tick it queries for active
// accounts and submits a FetchJob for each account that is due for polling.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("scheduler already running")
	}
	s.running = true
	s.mu.Unlock()

	s.log.Info("scheduler started", "interval", s.interval)

	// Run immediately on start, then tick every interval
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// Initial run after short delay to let system settle
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-time.After(5 * time.Second):
			s.tick(ctx)
		}

		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				s.log.Debug("scheduler shutting down: context cancelled")
				return
			case <-s.stopCh:
				s.log.Debug("scheduler shutting down: stop signal")
				return
			case <-ticker.C:
				s.tick(ctx)
			}
		}
	}()

	return nil
}

// tick performs a single scheduling cycle: query accounts, submit jobs.
func (s *Scheduler) tick(ctx context.Context) {
	start := time.Now()
	log := s.log.With("tick", start.Format(time.RFC3339))

	accounts, err := s.queryDueAccounts(ctx)
	if err != nil {
		log.Error("failed to query due accounts", "error", err)
		return
	}

	if len(accounts) == 0 {
		log.Debug("no accounts due for polling")
		return
	}

	log.Info("scheduling poll jobs", "accounts", len(accounts))

	var submitted, dropped int
	for _, acct := range accounts {
		job := FetchJob{
			AccountID: acct.ID,
			UserID:    acct.UserID,
			Provider:  acct.Provider,
		}

		if s.pool.Submit(job) {
			submitted++
			// Update last_polled_at
			if err := s.updateLastPolled(ctx, acct.ID); err != nil {
				log.Error("failed to update last_polled_at", "account_id", acct.ID, "error", err)
			}
		} else {
			dropped++
			log.Warn("job dropped: worker pool queue full", "account_id", acct.ID)
		}
	}

	log.Info("tick complete",
		"submitted", submitted,
		"dropped", dropped,
		"duration", time.Since(start),
	)
}

// queryDueAccounts fetches active email accounts that are due for polling.
// An account is due if: is_active=true AND (last_polled_at IS NULL OR
// last_polled_at + poll_interval <= NOW()).
func (s *Scheduler) queryDueAccounts(ctx context.Context) ([]AccountRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			ea.id,
			ea.user_id,
			ea.provider,
			COALESCE(ea.poll_interval, $1)::bigint as poll_interval_ms,
			ea.is_active,
			ea.created_at,
			ea.last_polled_at
		FROM email_accounts ea
		WHERE ea.is_active = true
			AND (
				ea.last_polled_at IS NULL
				OR ea.last_polled_at + COALESCE(ea.poll_interval, $1) * INTERVAL '1 millisecond' <= NOW()
			)
		ORDER BY
			CASE WHEN ea.last_polled_at IS NULL THEN 0 ELSE 1 END,
			ea.last_polled_at ASC NULLS FIRST
		LIMIT 1000
	`, s.interval.Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("query due accounts: %w", err)
	}
	defer rows.Close()

	var accounts []AccountRow
	for rows.Next() {
		var acct AccountRow
		var intervalMs int64
		var lastPolled sql.NullTime

		err := rows.Scan(
			&acct.ID,
			&acct.UserID,
			&acct.Provider,
			&intervalMs,
			&acct.IsActive,
			&acct.CreatedAt,
			&lastPolled,
		)
		if err != nil {
			s.log.Error("failed to scan account row", "error", err)
			continue
		}

		acct.PollInterval = time.Duration(intervalMs) * time.Millisecond
		if lastPolled.Valid {
			acct.LastPolledAt = &lastPolled.Time
		}

		accounts = append(accounts, acct)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account rows: %w", err)
	}

	return accounts, nil
}

// updateLastPolled updates the last_polled_at timestamp for an account.
func (s *Scheduler) updateLastPolled(ctx context.Context, accountID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE email_accounts SET last_polled_at = $1 WHERE id = $2`,
		time.Now().UTC(), accountID,
	)
	return err
}

// Stop halts the scheduler and waits for the current tick to complete.
func (s *Scheduler) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return fmt.Errorf("scheduler not running")
	}
	s.running = false
	s.mu.Unlock()

	close(s.stopCh)

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.log.Info("scheduler stopped gracefully")
		return nil
	case <-time.After(30 * time.Second):
		s.log.Warn("scheduler stop timed out")
		return fmt.Errorf("scheduler stop timed out after 30s")
	}
}

// IsRunning returns true if the scheduler is currently running.
func (s *Scheduler) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}
