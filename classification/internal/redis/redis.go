// Package redis provides a Redis client wrapper for caching and rate-limiting.
package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps go-redis/v9 with application-specific helpers.
type Client struct {
	client *redis.Client
}

// New creates a new Redis client.
func New(addr, password string, db int) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		PoolSize:     20,
		MinIdleConns: 5,
		MaxRetries:   3,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &Client{client: rdb}, nil
}

// Close shuts down the Redis client.
func (c *Client) Close() error {
	return c.client.Close()
}

// RawClient returns the underlying go-redis client for use with packages
// that require the raw *redis.Client (e.g. rate-limit middleware).
func (c *Client) RawClient() *redis.Client {
	return c.client
}

// Health checks connectivity.
func (c *Client) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return c.client.Ping(ctx).Err()
}

// GetBytes retrieves a byte slice by key.
func (c *Client) GetBytes(ctx context.Context, key string) ([]byte, error) {
	return c.client.Get(ctx, key).Bytes()
}

// SetBytes stores a byte slice with TTL.
func (c *Client) SetBytes(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

// Delete removes a key.
func (c *Client) Delete(ctx context.Context, keys ...string) error {
	return c.client.Del(ctx, keys...).Err()
}

// GetActiveRulesHash returns a cached hash of active rules for cache-busting.
func (c *Client) GetActiveRulesHash(ctx context.Context, userID string) (string, error) {
	key := fmt.Sprintf("rules_hash:%s", userID)
	return c.client.Get(ctx, key).Result()
}

// SetActiveRulesHash caches the active rules hash.
func (c *Client) SetActiveRulesHash(ctx context.Context, userID string, hash string, ttl time.Duration) error {
	key := fmt.Sprintf("rules_hash:%s", userID)
	return c.client.Set(ctx, key, hash, ttl).Err()
}

// IncrUsageCount atomically increments rule usage count in Redis (async flush to DB).
func (c *Client) IncrUsageCount(ctx context.Context, ruleID string) error {
	key := fmt.Sprintf("rule_usage:%s", ruleID)
	return c.client.Incr(ctx, key).Err()
}

// GetUsageCounts returns all pending usage count increments.
func (c *Client) GetUsageCounts(ctx context.Context, pattern string) (map[string]int64, error) {
	var cursor uint64
	result := make(map[string]int64)
	for {
		keys, next, err := c.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			val, err := c.client.Get(ctx, key).Int64()
			if err != nil {
				continue
			}
			result[key] = val
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return result, nil
}
