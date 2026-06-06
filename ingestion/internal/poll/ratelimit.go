package poll

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/decisionstack/ingestion/internal/models"

	"github.com/redis/go-redis/v9"
)

// RateLimiter provides Redis-backed rate limiting and quota tracking for
// Gmail and Outlook API calls. It uses atomic Lua scripts to ensure
// correctness under concurrent access.
//
// Gmail: 250 quota units / user / second
// Outlook: 10,000 requests / 10 minutes / app
type RateLimiter struct {
	redis redis.UniversalClient
}

// NewRateLimiter creates a new RateLimiter backed by the given Redis client.
func NewRateLimiter(redis redis.UniversalClient) *RateLimiter {
	return &RateLimiter{redis: redis}
}

// ---------------------------------------------------------------------------
// Gmail Rate Limiting — 250 units / user / second
// ---------------------------------------------------------------------------

// gmailAllowScript is a Lua script that atomically checks and decrements the
// Gmail quota. It returns {allowed, remaining, reset_at_ms}.
// Keys: [quota_key]
// Args: [cost, window_ms, limit]
var gmailAllowScript = redis.NewScript(`
	local key = KEYS[1]
	local cost = tonumber(ARGV[1])
	local window_ms = tonumber(ARGV[2])
	local limit = tonumber(ARGV[3])

	local now_ms = redis.call('TIME')
	now_ms = tonumber(now_ms[1]) * 1000 + tonumber(now_ms[2]) / 1000

	local reset_at_ms = now_ms + window_ms

	-- Get current remaining, or initialize if key doesn't exist or expired
	local remaining = redis.call('GET', key)
	if remaining == false then
		-- Key doesn't exist: initialize with full quota
		remaining = limit - cost
		if remaining < 0 then
			return {0, limit, reset_at_ms}
		end
		redis.call('SET', key, remaining, 'PX', window_ms)
		return {1, remaining, reset_at_ms}
	end

	remaining = tonumber(remaining)
	if remaining < cost then
		return {0, remaining, reset_at_ms}
	end

	remaining = remaining - cost
	redis.call('SET', key, remaining, 'KEEPTTL')
	return {1, remaining, reset_at_ms}
`)

// AllowGmailRequest checks if a Gmail API request with the given cost is
// allowed under the per-user quota of 250 units/second.
//
// Key format: ratelimit:gmail:{user_id}
// Returns RateLimitStatus with Allowed=false and Backoff set if over quota.
func (rl *RateLimiter) AllowGmailRequest(ctx context.Context, userID string, cost int) (*models.RateLimitStatus, error) {
	if cost <= 0 {
		cost = 1
	}

	key := fmt.Sprintf("ratelimit:gmail:%s", userID)
	windowMs := 1000 // 1 second in milliseconds

	result, err := gmailAllowScript.Run(ctx, rl.redis, []string{key}, cost, windowMs, models.GmailQuotaUnitsPerSecond).Result()
	if err != nil {
		return nil, fmt.Errorf("gmail rate limit check failed: %w", err)
	}

	arr := result.([]interface{})
	allowed := arr[0].(int64) == 1
	remaining := int(arr[1].(int64))
	resetAtMs := int64(arr[2].(int64))
	resetAt := time.UnixMilli(resetAtMs)

	status := &models.RateLimitStatus{
		Allowed:   allowed,
		Remaining: remaining,
		ResetAt:   resetAt,
	}

	if !allowed {
		status.Backoff = time.Until(resetAt)
		if status.Backoff < 0 {
			status.Backoff = 0
		}
	}

	return status, nil
}

// RefundGmailQuota refunds (increments) the Gmail quota by the given amount.
// Used when a request fails and the quota should be returned.
func (rl *RateLimiter) RefundGmailQuota(ctx context.Context, userID string, amount int) error {
	key := fmt.Sprintf("ratelimit:gmail:%s", userID)
	pipe := rl.redis.Pipeline()
	pipe.IncrBy(ctx, key, int64(amount))
	// Ensure TTL exists; if key was deleted, reset with full window
	pipe.PTTL(ctx, key)
	results, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("refund gmail quota: %w", err)
	}

	// Check TTL; if -1 (no expiry) or -2 (key doesn't exist), set expiry
	ttlResult := results[1].(*redis.DurationCmd)
	ttl := ttlResult.Val()
	if ttl <= 0 {
		_ = rl.redis.Expire(ctx, key, time.Second).Err()
	}

	return nil
}

// ResetGmailQuota resets the Gmail quota to full (250 units) for a user.
// This should be called once per second by a background timer or cron.
func (rl *RateLimiter) ResetGmailQuota(ctx context.Context, userID string) error {
	key := fmt.Sprintf("ratelimit:gmail:%s", userID)
	return rl.redis.Set(ctx, key, models.GmailQuotaUnitsPerSecond, time.Second).Err()
}

// GetGmailQuota returns the current remaining quota for a user.
func (rl *RateLimiter) GetGmailQuota(ctx context.Context, userID string) (int, error) {
	key := fmt.Sprintf("ratelimit:gmail:%s", userID)
	val, err := rl.redis.Get(ctx, key).Result()
	if err == redis.Nil {
		return models.GmailQuotaUnitsPerSecond, nil
	}
	if err != nil {
		return 0, err
	}
	remaining, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("parse quota: %w", err)
	}
	return remaining, nil
}

// ---------------------------------------------------------------------------
// Outlook Rate Limiting — 10,000 requests / 10 minutes / app
// ---------------------------------------------------------------------------

// outlookAllowScript is a Lua script that atomically checks and decrements the
// Outlook quota. It returns {allowed, remaining, reset_at_ms}.
// Keys: [quota_key]
// Args: [cost, window_ms, limit]
var outlookAllowScript = redis.NewScript(`
	local key = KEYS[1]
	local cost = tonumber(ARGV[1])
	local window_ms = tonumber(ARGV[2])
	local limit = tonumber(ARGV[3])

	local now_ms = redis.call('TIME')
	now_ms = tonumber(now_ms[1]) * 1000 + tonumber(now_ms[2]) / 1000

	local reset_at_ms = now_ms + window_ms

	-- Get current remaining, or initialize if key doesn't exist or expired
	local remaining = redis.call('GET', key)
	if remaining == false then
		-- Key doesn't exist: initialize with full quota
		remaining = limit - cost
		if remaining < 0 then
			return {0, limit, reset_at_ms}
		end
		redis.call('SET', key, remaining, 'PX', window_ms)
		return {1, remaining, reset_at_ms}
	end

	remaining = tonumber(remaining)
	if remaining < cost then
		return {0, remaining, reset_at_ms}
	end

	remaining = remaining - cost
	redis.call('SET', key, remaining, 'KEEPTTL')
	return {1, remaining, reset_at_ms}
`)

// AllowOutlookRequest checks if an Outlook API request is allowed under the
// per-app quota of 10,000 requests per 10 minutes.
//
// Key format: ratelimit:outlook:{app_id}
// Returns RateLimitStatus with Allowed=false and Backoff set if over quota.
func (rl *RateLimiter) AllowOutlookRequest(ctx context.Context, appID string) (*models.RateLimitStatus, error) {
	key := fmt.Sprintf("ratelimit:outlook:%s", appID)
	windowMs := 10 * 60 * 1000 // 10 minutes in milliseconds
	cost := 1                  // Outlook counts requests, not quota units

	result, err := outlookAllowScript.Run(ctx, rl.redis, []string{key}, cost, windowMs, models.OutlookRequestsPer10Min).Result()
	if err != nil {
		return nil, fmt.Errorf("outlook rate limit check failed: %w", err)
	}

	arr := result.([]interface{})
	allowed := arr[0].(int64) == 1
	remaining := int(arr[1].(int64))
	resetAtMs := int64(arr[2].(int64))
	resetAt := time.UnixMilli(resetAtMs)

	status := &models.RateLimitStatus{
		Allowed:   allowed,
		Remaining: remaining,
		ResetAt:   resetAt,
	}

	if !allowed {
		status.Backoff = time.Until(resetAt)
		if status.Backoff < 0 {
			status.Backoff = 0
		}
		if status.Backoff > 10*time.Minute {
			status.Backoff = 10 * time.Minute
		}
	}

	return status, nil
}

// RefundOutlookQuota refunds (increments) the Outlook quota by 1.
func (rl *RateLimiter) RefundOutlookQuota(ctx context.Context, appID string) error {
	key := fmt.Sprintf("ratelimit:outlook:%s", appID)
	pipe := rl.redis.Pipeline()
	pipe.Incr(ctx, key)
	pipe.PTTL(ctx, key)
	results, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("refund outlook quota: %w", err)
	}

	ttlResult := results[1].(*redis.DurationCmd)
	ttl := ttlResult.Val()
	if ttl <= 0 {
		_ = rl.redis.Expire(ctx, key, 10*time.Minute).Err()
	}

	return nil
}

// ResetOutlookQuota resets the Outlook quota to full (10,000 requests).
// Called at application startup or when the 10-minute window rolls over.
func (rl *RateLimiter) ResetOutlookQuota(ctx context.Context, appID string) error {
	key := fmt.Sprintf("ratelimit:outlook:%s", appID)
	return rl.redis.Set(ctx, key, models.OutlookRequestsPer10Min, 10*time.Minute).Err()
}

// GetOutlookQuota returns the current remaining quota for an app.
func (rl *RateLimiter) GetOutlookQuota(ctx context.Context, appID string) (int, error) {
	key := fmt.Sprintf("ratelimit:outlook:%s", appID)
	val, err := rl.redis.Get(ctx, key).Result()
	if err == redis.Nil {
		return models.OutlookRequestsPer10Min, nil
	}
	if err != nil {
		return 0, err
	}
	remaining, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("parse quota: %w", err)
	}
	return remaining, nil
}

// ---------------------------------------------------------------------------
// Batch Gmail Cost Tracking
// ---------------------------------------------------------------------------

// TrackGmailCosts atomically decrements the Gmail quota by the total cost of
// multiple operations. Use this when you know the total cost upfront.
func (rl *RateLimiter) TrackGmailCosts(ctx context.Context, userID string, totalCost int) (*models.RateLimitStatus, error) {
	if totalCost <= 0 {
		return &models.RateLimitStatus{Allowed: true, Remaining: models.GmailQuotaUnitsPerSecond}, nil
	}
	return rl.AllowGmailRequest(ctx, userID, totalCost)
}
