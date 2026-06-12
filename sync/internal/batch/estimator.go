// Package batch provides clear-time estimation based on per-user running averages.
package batch

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/decisionstack/sync/internal/logger"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	// TimingKeyPrefix is the Redis key prefix for per-user average seconds-per-card.
	TimingKeyPrefix = "timing:user"

	// DefaultSecondsPerCard is the assumed average when no history exists.
	DefaultSecondsPerCard = 45.0

	// EMAAlpha is the exponential moving average smoothing factor.
	// Higher = more responsive to recent data. 0.2 means ~5 recent samples dominate.
	EMAAlpha = 0.2

	// MaxSecondsPerCard caps the per-card time to prevent outliers from skewing estimates.
	MaxSecondsPerCard = 600.0

	// MinSecondsPerCard prevents unrealistically low estimates.
	MinSecondsPerCard = 5.0
)

// estimatorRedis is the minimal Redis interface the estimator requires.
type estimatorRedis interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
}

// ClearTimeEstimator computes batch clear-time estimates using a per-user
// exponential moving average (EMA) stored in Redis.
type ClearTimeEstimator struct {
	redis estimatorRedis
}

// NewClearTimeEstimator creates a new estimator backed by Redis.
func NewClearTimeEstimator(redis estimatorRedis) *ClearTimeEstimator {
	return &ClearTimeEstimator{redis: redis}
}

// timingKey returns the Redis key for a user's average-seconds-per-card.
func timingKey(userID uuid.UUID) string {
	return fmt.Sprintf("%s:%s:avg_seconds_per_card", TimingKeyPrefix, userID.String())
}

// lastClearKey returns the Redis key tracking when a user last cleared a card.
func lastClearKey(userID uuid.UUID) string {
	return fmt.Sprintf("%s:%s:last_cleared_at", TimingKeyPrefix, userID.String())
}

// Estimate returns estimated minutes to clear pendingCount cards.
//
// Formula: pendingCount * avg_seconds_per_card / 60 = minutes
//
// If no timing history exists for the user, DefaultSecondsPerCard (45s) is used.
// The result is rounded up and clamped to a minimum of 1 minute.
func (e *ClearTimeEstimator) Estimate(ctx context.Context, userID uuid.UUID, pendingCount int) int {
	if pendingCount <= 0 {
		return 0
	}

	avg := e.getAverageSeconds(ctx, userID)

	minutes := float64(pendingCount) * avg / 60.0
	estimate := int(math.Ceil(minutes))
	if estimate < 1 {
		estimate = 1
	}

	logger.Debug("clear time estimated",
		"user_id", userID,
		"pending_count", pendingCount,
		"avg_seconds", avg,
		"estimated_minutes", estimate,
	)

	return estimate
}

// RecordCardCleared updates the running average with a new data point.
// elapsedSeconds is the wall-clock time the user spent on the card.
//
// Uses exponential moving average: avg' = alpha * sample + (1 - alpha) * avg
// The elapsed time is clamped to [MinSecondsPerCard, MaxSecondsPerCard]
// to prevent extreme outliers from distorting the average.
func (e *ClearTimeEstimator) RecordCardCleared(ctx context.Context, userID uuid.UUID, elapsedSeconds float64) {
	// Clamp to sensible bounds.
	if elapsedSeconds < MinSecondsPerCard {
		elapsedSeconds = MinSecondsPerCard
	}
	if elapsedSeconds > MaxSecondsPerCard {
		elapsedSeconds = MaxSecondsPerCard
	}

	avg := e.getAverageSeconds(ctx, userID)
	newAvg := EMAAlpha*elapsedSeconds + (1.0-EMAAlpha)*avg

	key := timingKey(userID)
	if err := e.redis.Set(ctx, key, strconv.FormatFloat(newAvg, 'f', 6, 64), 30*24*time.Hour).Err(); err != nil {
		logger.Error("failed to store timing average", "error", err, "user_id", userID)
		return
	}

	// Also record when the user last cleared a card.
	lastKey := lastClearKey(userID)
	if err := e.redis.Set(ctx, lastKey, strconv.FormatInt(time.Now().Unix(), 10), 30*24*time.Hour).Err(); err != nil {
		logger.Warn("failed to store last clear timestamp", "error", err, "user_id", userID)
	}

	logger.Debug("timing recorded",
		"user_id", userID,
		"elapsed_seconds", elapsedSeconds,
		"old_avg", avg,
		"new_avg", newAvg,
	)
}

// GetAverageSeconds returns the current average seconds per card for a user.
// Returns DefaultSecondsPerCard if no history exists.
func (e *ClearTimeEstimator) GetAverageSeconds(ctx context.Context, userID uuid.UUID) float64 {
	return e.getAverageSeconds(ctx, userID)
}

// getAverageSeconds is the internal helper that fetches from Redis.
func (e *ClearTimeEstimator) getAverageSeconds(ctx context.Context, userID uuid.UUID) float64 {
	key := timingKey(userID)
	val, err := e.redis.Get(ctx, key).Result()
	if err == redis.Nil {
		return DefaultSecondsPerCard
	}
	if err != nil {
		logger.Error("failed to read timing average", "error", err, "user_id", userID)
		return DefaultSecondsPerCard
	}

	avg, err := strconv.ParseFloat(val, 64)
	if err != nil {
		logger.Warn("invalid timing average in redis", "error", err, "value", val)
		return DefaultSecondsPerCard
	}

	// Sanity clamp.
	if avg < MinSecondsPerCard {
		return MinSecondsPerCard
	}
	if avg > MaxSecondsPerCard {
		return MaxSecondsPerCard
	}

	return avg
}

// Reset clears the timing history for a user.
func (e *ClearTimeEstimator) Reset(ctx context.Context, userID uuid.UUID) error {
	key := timingKey(userID)
	lastKey := lastClearKey(userID)
	if err := e.redis.Del(ctx, key, lastKey).Err(); err != nil {
		return fmt.Errorf("reset timing data: %w", err)
	}
	return nil
}

// GetLastClearedAt returns the timestamp when the user last cleared a card,
// or zero time if never recorded.
func (e *ClearTimeEstimator) GetLastClearedAt(ctx context.Context, userID uuid.UUID) time.Time {
	key := lastClearKey(userID)
	val, err := e.redis.Get(ctx, key).Result()
	if err != nil {
		return time.Time{}
	}

	unixSec, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return time.Time{}
	}

	return time.Unix(unixSec, 0).UTC()
}
