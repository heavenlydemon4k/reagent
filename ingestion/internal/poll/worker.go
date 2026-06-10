// Package poll provides the polling worker pool that fetches emails when
// webhooks fail or for initial historical sync. This is the fallback mechanism
// that ensures zero email loss.
package poll

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/decisionstack/ingestion/internal/models"
	natsevents "github.com/decisionstack/ingestion/internal/nats"
	"github.com/google/uuid"
)

// EmailAssembler orchestrates thread resolution, contact dedup, and
// raw_emails persistence for a single parsed email.
// Production: events.Assembler. Testing: mock.
type EmailAssembler interface {
	AssembleEvent(ctx context.Context, email *models.ParsedEmail, rawEmailID uuid.UUID, s3URI string) (*natsevents.EmailIngestedEvent, error)
}

// FetchJob represents a single unit of work: poll one email account.
type FetchJob struct {
	AccountID uuid.UUID
	UserID    uuid.UUID
	Provider  string // "gmail" | "outlook"
}

// JobProcessor is the interface that must be implemented by GmailPoller and
// OutlookPoller. Each worker in the pool calls Process for every FetchJob.
type JobProcessor interface {
	Process(ctx context.Context, job FetchJob) error
}

// WorkerPool manages a fixed number of goroutines that consume FetchJobs from
// a buffered channel. It provides non-blocking submission and graceful shutdown.
type WorkerPool struct {
	size int
	jobs chan FetchJob
	wg   sync.WaitGroup
	log  *slog.Logger

	// stopCh signals workers to exit immediately (used during Stop()).
	stopCh chan struct{}

	// mu protects the running flag.
	mu      sync.Mutex
	running bool
}

// NewWorkerPool creates a new worker pool with the given size and logger.
// The size determines the maximum number of concurrent polling operations.
func NewWorkerPool(size int, log *slog.Logger) *WorkerPool {
	if size <= 0 {
		size = 4 // sensible default
	}
	return &WorkerPool{
		size:   size,
		jobs:   make(chan FetchJob, size*4), // 4x buffer for non-blocking submit
		log:    log.With("component", "worker_pool"),
		stopCh: make(chan struct{}),
	}
}

// Start launches N worker goroutines that consume from the jobs channel.
// Each worker runs until the provided context is cancelled or Stop() is called.
func (wp *WorkerPool) Start(ctx context.Context, processor JobProcessor) {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	if wp.running {
		wp.log.Warn("worker pool already running")
		return
	}
	wp.running = true

	for i := range wp.size {
		wp.wg.Add(1)
		go wp.worker(ctx, i, processor)
	}

	wp.log.Info("worker pool started", "size", wp.size)
}

// worker is the main loop for each goroutine in the pool.
func (wp *WorkerPool) worker(ctx context.Context, id int, processor JobProcessor) {
	defer wp.wg.Done()

	log := wp.log.With("worker_id", id)
	log.Info("worker started")

	for {
		select {
		case <-ctx.Done():
			log.Debug("worker shutting down: context cancelled")
			return
		case <-wp.stopCh:
			log.Debug("worker shutting down: stop signal")
			return
		case job, ok := <-wp.jobs:
			if !ok {
				log.Debug("worker shutting down: jobs channel closed")
				return
			}
			log.Debug("processing job",
				"account_id", job.AccountID,
				"provider", job.Provider,
			)
			start := time.Now()
			if err := processor.Process(ctx, job); err != nil {
				log.Error("job failed",
					"account_id", job.AccountID,
					"provider", job.Provider,
					"error", err,
					"duration", time.Since(start),
				)
			} else {
				log.Debug("job completed",
					"account_id", job.AccountID,
					"provider", job.Provider,
					"duration", time.Since(start),
				)
			}
		}
	}
}

// Stop waits for all workers to finish processing their current job, then
// returns. It signals workers to stop accepting new jobs.
func (wp *WorkerPool) Stop() error {
	wp.mu.Lock()
	if !wp.running {
		wp.mu.Unlock()
		return errors.New("worker pool not running")
	}
	wp.running = false
	wp.mu.Unlock()

	// Signal all workers to stop accepting new jobs and exit.
	close(wp.stopCh)

	// Drain remaining jobs so workers don't block on channel read.
	go func() {
		for range wp.jobs {
			// discard remaining jobs
		}
	}()

	// Wait for all workers to finish.
	done := make(chan struct{})
	go func() {
		wp.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		wp.log.Info("worker pool stopped gracefully")
		return nil
	case <-time.After(30 * time.Second):
		wp.log.Warn("worker pool stop timed out")
		return errors.New("worker pool stop timed out after 30s")
	}
}

// Submit adds a FetchJob to the work queue. It is non-blocking: if the channel
// buffer is full it returns false immediately.
func (wp *WorkerPool) Submit(job FetchJob) bool {
	select {
	case wp.jobs <- job:
		wp.log.Debug("job submitted", "account_id", job.AccountID, "provider", job.Provider)
		return true
	default:
		wp.log.Warn("job submission dropped: queue full", "account_id", job.AccountID)
		return false
	}
}

// Pending returns the number of jobs currently queued (not yet being processed).
func (wp *WorkerPool) Pending() int {
	return len(wp.jobs)
}
