package webhook

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// dedupKeyPrefix is the Redis key prefix for webhook deduplication.
	dedupKeyPrefix = "dedup:webhook"
	// dedupTTL is the time-to-live for dedup entries (24 hours).
	dedupTTL = 24 * time.Hour
)

// DedupChecker provides Redis-based deduplication for webhook notifications.
type DedupChecker struct {
	redis redis.Cmdable
}

// NewDedupChecker creates a new DedupChecker.
func NewDedupChecker(redisClient redis.Cmdable) *DedupChecker {
	return &DedupChecker{redis: redisClient}
}

// IsDuplicate checks if the given key already exists in Redis.
// It uses SET NX (set if not exists) with a 24-hour TTL.
// Returns true if the key already exists (duplicate), false if it's new.
func (d *DedupChecker) IsDuplicate(ctx context.Context, key string) (bool, error) {
	fullKey := fmt.Sprintf("%s:%s", dedupKeyPrefix, key)

	// SET key "1" NX EX ttl
	// Returns "OK" if set, nil if key already exists
	set, err := d.redis.SetNX(ctx, fullKey, "1", dedupTTL).Result()
	if err != nil {
		return false, fmt.Errorf("redis setnx: %w", err)
	}

	// SETNX returns true if the key was set (new), false if it already existed (duplicate)
	return !set, nil
}

// DedupKeyGmail creates a dedup key for Gmail webhooks based on history ID.
func DedupKeyGmail(historyID uint64) string {
	return fmt.Sprintf("gmail:%d", historyID)
}

// DedupKeyOutlook creates a dedup key for Outlook webhooks based on notification ID.
func DedupKeyOutlook(notificationID string) string {
	return fmt.Sprintf("outlook:%s", notificationID)
}
