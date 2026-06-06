// Package events handles event publishing for the Ingestion Mesh.
// publisher.go wraps the shared NATS Publisher with retry and batch support.
package events

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	natspkg "github.com/decisionstack/ingestion/internal/nats"
)

const (
	// maxPublishRetries is the number of attempts before giving up on a single event.
	maxPublishRetries = 3
	// retryBaseDelay is the initial backoff between retries.
	retryBaseDelay = 500 * time.Millisecond
	// retryMaxDelay caps the exponential backoff.
	retryMaxDelay = 5 * time.Second
	// batchWorkerTimeout is the max time to wait for a batch to complete.
	batchWorkerTimeout = 30 * time.Second
)

// EventPublisher wraps the shared nats.Publisher with logging and helpers.
type EventPublisher struct {
	nats natspkg.Publisher
	log  *slog.Logger
}

// NewEventPublisher creates a new event publisher wrapper.
func NewEventPublisher(nats natspkg.Publisher, log *slog.Logger) *EventPublisher {
	if log == nil {
		log = slog.Default()
	}
	return &EventPublisher{nats: nats, log: log}
}

// Publish publishes a single email.ingested event to NATS with retry logic.
// It attempts up to maxPublishRetries with exponential backoff. If all retries
// fail, the error is returned and the caller decides DLQ handling.
func (p *EventPublisher) Publish(ctx context.Context, event *natspkg.EmailIngestedEvent) error {
	if event == nil {
		return fmt.Errorf("event is nil")
	}

	var lastErr error
	for attempt := 0; attempt < maxPublishRetries; attempt++ {
		if attempt > 0 {
			delay := retryBaseDelay * time.Duration(1<<uint(attempt-1))
			if delay > retryMaxDelay {
				delay = retryMaxDelay
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("publish cancelled after %d attempts: %w", attempt, ctx.Err())
			case <-time.After(delay):
			}
		}

		err := p.nats.PublishEmailIngested(ctx, *event)
		if err == nil {
			p.log.Debug("event published",
				"event_id", event.EventID,
				"thread_id", event.ThreadID,
				"attempt", attempt+1,
			)
			return nil
		}

		lastErr = err
		p.log.Warn("publish attempt failed",
			"event_id", event.EventID,
			"attempt", attempt+1,
			"error", err,
		)
	}

	return fmt.Errorf("publish failed after %d retries: %w", maxPublishRetries, lastErr)
}

// PublishResult is the outcome of a single event publish within a batch.
type PublishResult struct {
	Event   *natspkg.EmailIngestedEvent
	Error   error
	Success bool
}

// PublishBatch publishes multiple events concurrently and reports per-event results.
// It uses a worker pool to limit concurrency. Failures are returned in the results
// slice — the caller decides retry/DLQ policy.
func (p *EventPublisher) PublishBatch(ctx context.Context, events []*natspkg.EmailIngestedEvent) ([]PublishResult, error) {
	if len(events) == 0 {
		return nil, nil
	}

	// Limit concurrency to avoid overwhelming NATS
	const maxWorkers = 10
	workerCount := len(events)
	if workerCount > maxWorkers {
		workerCount = maxWorkers
	}

	type job struct {
		index int
		event *natspkg.EmailIngestedEvent
	}

	var wg sync.WaitGroup
	jobs := make(chan job, len(events))
	results := make([]PublishResult, len(events))

	// Start workers
	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				err := p.Publish(ctx, j.event)
				results[j.index] = PublishResult{
					Event:   j.event,
					Error:   err,
					Success: err == nil,
				}
			}
		}()
	}

	// Enqueue all jobs
	for i, ev := range events {
		if ev == nil {
			results[i] = PublishResult{Error: fmt.Errorf("nil event at index %d", i)}
			continue
		}
		jobs <- job{index: i, event: ev}
	}
	close(jobs)

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// all done
	case <-ctx.Done():
		return results, fmt.Errorf("batch publish cancelled: %w", ctx.Err())
	case <-time.After(batchWorkerTimeout):
		return results, fmt.Errorf("batch publish timed out after %v", batchWorkerTimeout)
	}

	// Count failures
	failures := 0
	for _, r := range results {
		if !r.Success {
			failures++
		}
	}
	if failures > 0 {
		return results, fmt.Errorf("batch publish: %d/%d events failed", failures, len(events))
	}

	return results, nil
}
