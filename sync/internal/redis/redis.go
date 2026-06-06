// Package redis provides Redis client setup and utility functions for the sync service.
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/decisionstack/sync/internal/config"
	"github.com/decisionstack/sync/internal/logger"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Redis wraps go-redis Client with application-specific operations.
type Redis struct {
	client *redis.Client
	cfg    *config.Config
}

// New creates a new Redis client.
func New(cfg *config.Config) (*Redis, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
		PoolSize: 20,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("connect to redis: %w", err)
	}

	logger.Info("redis connected", "addr", cfg.RedisAddr)

	return &Redis{client: client, cfg: cfg}, nil
}

// Close closes the Redis connection.
func (r *Redis) Close() error {
	if r.client != nil {
		logger.Info("closing redis connection")
		return r.client.Close()
	}
	return nil
}

// Client returns the underlying go-redis client.
func (r *Redis) Client() *redis.Client {
	return r.client
}

// Health checks the Redis connectivity.
func (r *Redis) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return r.client.Ping(ctx).Err()
}

// ============================================================================
// QUEUE OPERATIONS (sorted set by server version)
// ============================================================================

const queueKeyPrefix = "queue:"

// QueueKey returns the Redis key for a user's queue.
func QueueKey(userID uuid.UUID) string {
	return queueKeyPrefix + userID.String()
}

// AddToQueue adds a card to the user's sorted set queue with server version as score.
func (r *Redis) AddToQueue(ctx context.Context, userID uuid.UUID, cardID uuid.UUID, serverVersion int) error {
	key := QueueKey(userID)
	member := redis.Z{
		Score:  float64(serverVersion),
		Member: cardID.String(),
	}
	return r.client.ZAdd(ctx, key, member).Err()
}

// RemoveFromQueue removes a card from the user's queue.
func (r *Redis) RemoveFromQueue(ctx context.Context, userID uuid.UUID, cardID uuid.UUID) error {
	key := QueueKey(userID)
	return r.client.ZRem(ctx, key, cardID.String()).Err()
}

// GetQueueRange returns cards in the user's queue within a version range.
func (r *Redis) GetQueueRange(ctx context.Context, userID uuid.UUID, minVersion, maxVersion int64) ([]uuid.UUID, error) {
	key := QueueKey(userID)
	members, err := r.client.ZRangeByScore(ctx, key, &redis.ZRangeBy{
		Min: fmt.Sprintf("%d", minVersion),
		Max: fmt.Sprintf("%d", maxVersion),
	}).Result()
	if err != nil {
		return nil, err
	}

	var cardIDs []uuid.UUID
	for _, m := range members {
		id, err := uuid.Parse(m)
		if err != nil {
			logger.Warn("invalid uuid in queue", "value", m, "user_id", userID)
			continue
		}
		cardIDs = append(cardIDs, id)
	}
	return cardIDs, nil
}

// QueueCount returns the number of cards in the user's queue.
func (r *Redis) QueueCount(ctx context.Context, userID uuid.UUID) (int64, error) {
	key := QueueKey(userID)
	return r.client.ZCard(ctx, key).Result()
}

// ============================================================================
// SERVER VERSION OPERATIONS (atomic counter)
// ============================================================================

const versionKeyPrefix = "version:"

// VersionKey returns the Redis key for a user's server version counter.
func VersionKey(userID uuid.UUID) string {
	return versionKeyPrefix + userID.String()
}

// IncrementVersion atomically increments and returns the server version.
func (r *Redis) IncrementVersion(ctx context.Context, userID uuid.UUID) (int64, error) {
	key := VersionKey(userID)
	return r.client.Incr(ctx, key).Result()
}

// GetVersion returns the current server version for a user.
func (r *Redis) GetVersion(ctx context.Context, userID uuid.UUID) (int64, error) {
	key := VersionKey(userID)
	v, err := r.client.Get(ctx, key).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return v, nil
}

// SetVersion sets the server version for a user.
func (r *Redis) SetVersion(ctx context.Context, userID uuid.UUID, version int64) error {
	key := VersionKey(userID)
	return r.client.Set(ctx, key, version, 0).Err()
}

// ============================================================================
// SYNC STATE OPERATIONS
// ============================================================================

const syncStatePrefix = "syncstate:"

// GetSyncState retrieves the last known sync state for a device.
func (r *Redis) GetSyncState(ctx context.Context, userID uuid.UUID, deviceID string) (int64, error) {
	key := syncStatePrefix + userID.String() + ":" + deviceID
	v, err := r.client.Get(ctx, key).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return v, err
}

// SetSyncState stores the last known sync state for a device.
func (r *Redis) SetSyncState(ctx context.Context, userID uuid.UUID, deviceID string, version int64) error {
	key := syncStatePrefix + userID.String() + ":" + deviceID
	return r.client.Set(ctx, key, version, 24*time.Hour).Err()
}

// ============================================================================
// RATE LIMITING
// ============================================================================

const rateLimitPrefix = "ratelimit:"

// CheckRateLimit checks if a key has exceeded the allowed count within the window.
// Returns true if the request is allowed, false if rate limited.
func (r *Redis) CheckRateLimit(ctx context.Context, key string, maxRequests int, window time.Duration) (bool, error) {
	fullKey := rateLimitPrefix + key
	now := time.Now().Unix()
	windowStart := now - int64(window.Seconds())

	pipe := r.client.Pipeline()
	zremRange := pipe.ZRemRangeByScore(ctx, fullKey, "0", fmt.Sprintf("%d", windowStart))
	zcard := pipe.ZCard(ctx, fullKey)
	pipe.ZAdd(ctx, fullKey, redis.Z{Score: float64(now), Member: now})
	pipe.Expire(ctx, fullKey, window)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, err
	}

	_ = zremRange
	count := zcard.Val() + 1
	return count <= int64(maxRequests), nil
}

// ============================================================================
// PUB/SUB — WebSocket event distribution
// ============================================================================

const wsChannelPrefix = "ws:"

// WSPublish publishes a WebSocket event to a user's channel.
func (r *Redis) WSPublish(ctx context.Context, userID uuid.UUID, event []byte) error {
	channel := wsChannelPrefix + userID.String()
	return r.client.Publish(ctx, channel, event).Err()
}

// WSSubscribe subscribes to a user's WebSocket channel.
func (r *Redis) WSSubscribe(ctx context.Context, userID uuid.UUID) *redis.PubSub {
	channel := wsChannelPrefix + userID.String()
	return r.client.Subscribe(ctx, channel)
}

// ============================================================================
// DEVICE TOKEN OPERATIONS
// ============================================================================

const devicePrefix = "device:"

// StoreDeviceToken stores a device's push notification token.
func (r *Redis) StoreDeviceToken(ctx context.Context, deviceID string, tokenData map[string]string) error {
	key := devicePrefix + deviceID
	data, err := json.Marshal(tokenData)
	if err != nil {
		return fmt.Errorf("marshal device token: %w", err)
	}
	return r.client.Set(ctx, key, data, 30*24*time.Hour).Err()
}

// GetDeviceToken retrieves a device's push notification token.
func (r *Redis) GetDeviceToken(ctx context.Context, deviceID string) (map[string]string, error) {
	key := devicePrefix + deviceID
	data, err := r.client.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var tokenData map[string]string
	if err := json.Unmarshal([]byte(data), &tokenData); err != nil {
		return nil, fmt.Errorf("unmarshal device token: %w", err)
	}
	return tokenData, nil
}
