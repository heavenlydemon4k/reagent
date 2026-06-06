// Package redis provides a Redis client wrapper with health checks and rate limiting
// helpers for the Ingestion Mesh.
package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/decisionstack/ingestion/internal/config"
	"github.com/redis/go-redis/v9"
)

// Client wraps go-redis/v9 with health checks and utility methods.
type Client struct {
	client *redis.Client
}

// New creates a new Redis client from configuration.
func New(cfg *config.Config) (*Client, error) {
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		// Fallback: try as host:port
		opts = &redis.Options{
			Addr:     cfg.RedisURL,
			PoolSize: cfg.RedisPoolSize,
		}
	} else {
		opts.PoolSize = cfg.RedisPoolSize
	}

	rdb := redis.NewClient(opts)

	// Verify connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		rdb.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &Client{client: rdb}, nil
}

// Client returns the underlying redis.Client.
func (c *Client) Client() *redis.Client {
	return c.client
}

// Ping checks Redis connectivity.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.client.Ping(ctx).Err()
}

// Close closes the Redis connection.
func (c *Client) Close() error {
	return c.client.Close()
}

// RateLimitAllow implements a sliding window rate limiter using Redis.
// Returns true if the request is allowed, false if rate limited.
// key: the rate limit bucket identifier (e.g., "ratelimit:gmail:{user_id}")
// limit: maximum number of requests allowed in the window
// window: the time window for the rate limit
func (c *Client) RateLimitAllow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	now := time.Now().Unix()
	windowStart := now - int64(window.Seconds())

	pipe := c.client.Pipeline()

	// Remove entries older than the window
	zremrange := pipe.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", windowStart))
	// Count current entries in the window
	zcount := pipe.ZCard(ctx, key)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("rate limit pipeline: %w", err)
	}

	if err := zremrange.Err(); err != nil {
		return false, fmt.Errorf("rate limit zremrange: %w", err)
	}

	current := zcount.Val()
	if int(current) >= limit {
		return false, nil
	}

	// Add current request to the window
	member := redis.Z{
		Score:  float64(now),
		Member: fmt.Sprintf("%d-%s", now, uuid()),
	}
	if err := c.client.ZAdd(ctx, key, member).Err(); err != nil {
		return false, fmt.Errorf("rate limit zadd: %w", err)
	}

	// Set expiry on the key to auto-cleanup
	c.client.Expire(ctx, key, window)

	return true, nil
}

func uuid() string {
	// Simple unique suffix for rate limit entries
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
