package poll

import (
	"sync"
	"time"
)

// Default backoff intervals: 5min -> 15min -> 1hr -> 6hr.
// These match the adaptive backoff requirement for the Ingestion Mesh.
var defaultBackoffIntervals = []time.Duration{
	5 * time.Minute,
	15 * time.Minute,
	1 * time.Hour,
	6 * time.Hour,
}

// BackoffStrategy implements adaptive backoff with a fixed sequence of intervals.
// It advances on failure and resets on success.
type BackoffStrategy struct {
	intervals []time.Duration
	current   int // index into intervals
	mu        sync.RWMutex
}

// NewBackoffStrategy creates a new backoff strategy with the default intervals.
func NewBackoffStrategy() *BackoffStrategy {
	return &BackoffStrategy{
		intervals: defaultBackoffIntervals,
		current:   0,
	}
}

// NewBackoffStrategyWithIntervals creates a backoff with custom intervals.
// Useful in tests.
func NewBackoffStrategyWithIntervals(intervals []time.Duration) *BackoffStrategy {
	// Defensive copy
	iv := make([]time.Duration, len(intervals))
	copy(iv, intervals)
	return &BackoffStrategy{
		intervals: iv,
		current:   0,
	}
}

// Next returns the current interval and advances to the next level (capped at
// the final interval). Call this when a failure occurs.
func (b *BackoffStrategy) Next() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	interval := b.intervals[b.current]
	if b.current < len(b.intervals)-1 {
		b.current++
	}
	return interval
}

// Reset sets the backoff to the first interval (5min). Call this when
// webhooks resume successfully or a polling cycle succeeds.
func (b *BackoffStrategy) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.current = 0
}

// Current returns the current backoff interval without advancing.
func (b *BackoffStrategy) Current() time.Duration {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.intervals[b.current]
}

// IsMaxed returns true if the backoff has reached the maximum interval.
func (b *BackoffStrategy) IsMaxed() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.current == len(b.intervals)-1
}
