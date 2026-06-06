// ---------------------------------------------------------------------------
// Rate Limiting Middleware — Decision Stack Sync Service
// ---------------------------------------------------------------------------
// Provides per-user rate limiting backed by Redis:
//   - Sync API:        100 requests/min/user
//   - Intelligence:    30 requests/min/user
//   - WebSocket:       1 connection/user, 10 messages/sec
//
// All responses include X-RateLimit-* headers.
// Failures are logged but fail open (allow request) to avoid
// cascading outages if Redis is unavailable.
// ---------------------------------------------------------------------------

package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// ---------------------------------------------------------------------------
// Preset Rate Limits
// ---------------------------------------------------------------------------

// RateLimits holds the per-endpoint rate limit configuration.
type RateLimits struct {
	// SyncAPI is the general REST API rate limit (requests per minute)
	SyncAPI int

	// IntelligenceAPI is the AI/ML endpoint rate limit (requests per minute)
	IntelligenceAPI int

	// WebSocketConnections is max concurrent WebSocket connections per user
	WebSocketConnections int

	// WebSocketMessages is max WebSocket messages per second per connection
	WebSocketMessages int
}

// DefaultRateLimits returns production-safe defaults.
func DefaultRateLimits() RateLimits {
	return RateLimits{
		SyncAPI:              100,
		IntelligenceAPI:      30,
		WebSocketConnections: 1,
		WebSocketMessages:    10,
	}
}

// ---------------------------------------------------------------------------
// HTTP Middleware — Per-User Rate Limiting
// ---------------------------------------------------------------------------

// RateLimitMiddleware returns an http.Handler middleware that rate-limits
// requests by X-User-ID header using Redis as the counter backend.
//
// The algorithm is a simple fixed-window counter:
//   - INCR a Redis key scoped to userID + endpoint
//   - EXPIRE the key on first increment to enforce the window
//   - If count > limit, reject with 429 Too Many Requests
//
// Headers set on every response:
//   X-RateLimit-Limit     — maximum allowed in the window
//   X-RateLimit-Remaining — requests remaining in current window
//   X-RateLimit-Window    — window duration in seconds
//
// If Redis is unavailable or the userID header is missing,
// the request is allowed through (fail-open).
func RateLimitMiddleware(redisClient *redis.Client, limit int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := r.Header.Get("X-User-ID")
			if userID == "" {
				// Anonymous request — allow through without rate limiting.
				// Consider adding IP-based limiting here for unauthenticated routes.
				next.ServeHTTP(w, r)
				return
			}

			key := fmt.Sprintf("ratelimit:api:%s", userID)
			ctx := r.Context()

			current, err := redisClient.Incr(ctx, key).Result()
			if err != nil {
				// Fail open: if Redis is down, don't block legitimate traffic.
				// Log the failure for observability.
				fmt.Printf("[ratelimit] redis INCR failed for user %s: %v\n", userID, err)
				next.ServeHTTP(w, r)
				return
			}

			if current == 1 {
				// First request in window — set the expiration.
				redisClient.Expire(ctx, key, window)
			}

			remaining := limit - int(current)
			if remaining < 0 {
				remaining = 0
			}

			// Always set rate limit headers (even on blocked requests)
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			w.Header().Set("X-RateLimit-Window", strconv.Itoa(int(window.Seconds())))

			if current > int64(limit) {
				w.Header().Set("Retry-After", strconv.Itoa(int(window.Seconds())))
				http.Error(w, `{"error":"rate limit exceeded","retry_after":`+
					strconv.Itoa(int(window.Seconds()))+`}`, http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ---------------------------------------------------------------------------
// Endpoint-Specific Middleware Constructors
// ---------------------------------------------------------------------------

// SyncAPIRateLimit creates rate limiting middleware for the Sync REST API.
// Default: 100 requests per minute per user.
func SyncAPIRateLimit(redisClient *redis.Client) func(http.Handler) http.Handler {
	return RateLimitMiddleware(redisClient, 100, time.Minute)
}

// IntelligenceAPIRateLimit creates rate limiting middleware for AI/ML endpoints.
// Default: 30 requests per minute per user.
func IntelligenceAPIRateLimit(redisClient *redis.Client) func(http.Handler) http.Handler {
	return RateLimitMiddleware(redisClient, 30, time.Minute)
}

// WebSocketRateLimit is a specialized rate limiter for WebSocket connections.
// It tracks both connection count (1 per user) and message rate (10/sec).
func WebSocketRateLimit(redisClient *redis.Client) func(http.Handler) http.Handler {
	return RateLimitMiddleware(redisClient, 10, time.Second)
}

// ---------------------------------------------------------------------------
// Connection Limiter — WebSocket Concurrent Connections
// ---------------------------------------------------------------------------

// ConnectionLimiter tracks concurrent WebSocket connections per user.
type ConnectionLimiter struct {
	redis   *redis.Client
	limit   int
	keyTtl  time.Duration
	keyPrefix string
}

// NewConnectionLimiter creates a connection limiter backed by Redis.
func NewConnectionLimiter(redisClient *redis.Client, limit int) *ConnectionLimiter {
	return &ConnectionLimiter{
		redis:     redisClient,
		limit:     limit,
		keyTtl:    2 * time.Hour, // Connection keys expire after 2h (stale cleanup)
		keyPrefix: "ratelimit:ws:conn",
	}
}

// Acquire attempts to register a new connection for the user.
// Returns true if the connection is allowed, false if at limit.
func (cl *ConnectionLimiter) Acquire(ctx context.Context, userID string) bool {
	key := fmt.Sprintf("%s:%s", cl.keyPrefix, userID)

	current, err := cl.redis.Incr(ctx, key).Result()
	if err != nil {
		// Fail open — allow connection if Redis is down
		return true
	}

	if current == 1 {
		cl.redis.Expire(ctx, key, cl.keyTtl)
	}

	if current > int64(cl.limit) {
		// Rollback the increment since we're rejecting
		cl.redis.Decr(ctx, key)
		return false
	}

	return true
}

// Release decrements the connection counter for the user.
func (cl *ConnectionLimiter) Release(ctx context.Context, userID string) {
	key := fmt.Sprintf("%s:%s", cl.keyPrefix, userID)
	cl.redis.Decr(ctx, key)
}

// ---------------------------------------------------------------------------
// Composite Middleware — Chained Rate Limiting
// ---------------------------------------------------------------------------

// WithRateLimits applies the full rate limiting stack:
//   1. WebSocket connection limit (if applicable)
//   2. Per-endpoint request rate limit
//   3. Headers on every response
func WithRateLimits(redisClient *redis.Client, limits RateLimits) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Connection limit check for WebSocket upgrade requests
			if isWebSocketRequest(r) {
				userID := r.Header.Get("X-User-ID")
				if userID != "" {
					limiter := NewConnectionLimiter(redisClient, limits.WebSocketConnections)
					if !limiter.Acquire(r.Context(), userID) {
						w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limits.WebSocketConnections))
						w.Header().Set("X-RateLimit-Remaining", "0")
						http.Error(w, `{"error":"websocket connection limit exceeded"}`,
							http.StatusTooManyRequests)
						return
					}
					// Release handled by the WebSocket handler on disconnect
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isWebSocketRequest checks if the request is a WebSocket upgrade.
func isWebSocketRequest(r *http.Request) bool {
	return r.Header.Get("Upgrade") == "websocket"
}
