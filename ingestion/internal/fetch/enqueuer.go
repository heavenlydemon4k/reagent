package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	ingestionnats "github.com/decisionstack/ingestion/internal/nats"
)

const (
	// redisQueuePrefix is the Redis list key prefix for per-user fetch queues.
	redisQueuePrefix = "fetch:queue"
	// jobTTL is how long completed jobs remain in the queue before expiring.
	jobTTL = 24 * time.Hour
)

// Enqueuer manages fetch job enqueueing and dequeuing via Redis lists.
type Enqueuer struct {
	redis     redis.Cmdable
	publisher ingestionnats.Publisher
	log       *slog.Logger
}

// NewEnqueuer creates a new Enqueuer.
func NewEnqueuer(redisClient redis.Cmdable, publisher ingestionnats.Publisher, log *slog.Logger) *Enqueuer {
	return &Enqueuer{
		redis:     redisClient,
		publisher: publisher,
		log:       log,
	}
}

// EnqueueFetchJob pushes a fetch job to the per-user Redis queue.
// The job is serialized as JSON and LPUSH'd onto the list.
func (e *Enqueuer) EnqueueFetchJob(ctx context.Context, job FetchJob) error {
	if job.ID == "" {
		return fmt.Errorf("fetch job ID is required")
	}
	if job.UserID == "" {
		return fmt.Errorf("fetch job UserID is required")
	}
	if job.AccountID == "" {
		return fmt.Errorf("fetch job AccountID is required")
	}
	if job.Source != "gmail" && job.Source != "outlook" {
		return fmt.Errorf("invalid source: %s (must be 'gmail' or 'outlook')", job.Source)
	}

	// Update enqueue timestamp
	job.EnqueuedAt = time.Now().UTC()

	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal fetch job: %w", err)
	}

	queueKey := queueKey(job.UserID)

	// Use LPUSH so jobs are added to the front, and BRPOP from the other end (RPOP semantics)
	// Actually use RPUSH for FIFO: first in, first out
	pipe := e.redis.Pipeline()
	pipe.RPush(ctx, queueKey, data)
	pipe.Expire(ctx, queueKey, jobTTL)

	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("redis rpush fetch job: %w", err)
	}

	e.log.DebugContext(ctx, "fetch job enqueued",
		slog.String("job_id", job.ID),
		slog.String("user_id", job.UserID),
		slog.String("source", job.Source),
	)

	return nil
}

// DequeueFetchJob performs a blocking pop from the per-user fetch queue.
// It uses BLPOP with a timeout to wait for jobs. Returns nil if timeout.
func (e *Enqueuer) DequeueFetchJob(ctx context.Context, userID string) (*FetchJob, error) {
	if userID == "" {
		return nil, fmt.Errorf("userID is required")
	}

	queueKey := queueKey(userID)

	result, err := e.redis.BLPop(ctx, 5*time.Second, queueKey).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil // timeout, no job available
		}
		return nil, fmt.Errorf("redis blpop: %w", err)
	}

	if len(result) < 2 {
		return nil, nil
	}

	var job FetchJob
	if err := json.Unmarshal([]byte(result[1]), &job); err != nil {
		return nil, fmt.Errorf("unmarshal fetch job: %w", err)
	}

	job.IncrementAttempts()

	e.log.DebugContext(ctx, "fetch job dequeued",
		slog.String("job_id", job.ID),
		slog.String("user_id", job.UserID),
		slog.String("source", job.Source),
		slog.Int("attempt", job.Attempts),
	)

	return &job, nil
}

// QueueLength returns the number of pending fetch jobs for a user.
func (e *Enqueuer) QueueLength(ctx context.Context, userID string) (int64, error) {
	if userID == "" {
		return 0, fmt.Errorf("userID is required")
	}

	count, err := e.redis.LLen(ctx, queueKey(userID)).Result()
	if err != nil {
		return 0, fmt.Errorf("redis llen: %w", err)
	}

	return count, nil
}

// DequeueAnyFetchJob attempts to dequeue from any available queue using
// blocking pop on multiple keys. This is useful for worker pools that
// process jobs from any user.
func (e *Enqueuer) DequeueAnyFetchJob(ctx context.Context, timeout time.Duration, userIDs ...string) (*FetchJob, string, error) {
	if len(userIDs) == 0 {
		return nil, "", nil
	}

	keys := make([]string, len(userIDs))
	for i, uid := range userIDs {
		keys[i] = queueKey(uid)
	}

	result, err := e.redis.BLPop(ctx, timeout, keys...).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, "", nil // timeout
		}
		return nil, "", fmt.Errorf("redis blpop any: %w", err)
	}

	if len(result) < 2 {
		return nil, "", nil
	}

	var job FetchJob
	if err := json.Unmarshal([]byte(result[1]), &job); err != nil {
		return nil, "", fmt.Errorf("unmarshal fetch job: %w", err)
	}

	job.IncrementAttempts()

	// Extract userID from the queue key
	userID := ""
	for _, uid := range userIDs {
		if queueKey(uid) == result[0] {
			userID = uid
			break
		}
	}

	e.log.DebugContext(ctx, "fetch job dequeued (any queue)",
		slog.String("job_id", job.ID),
		slog.String("user_id", job.UserID),
		slog.String("source", job.Source),
	)

	return &job, userID, nil
}

// queueKey returns the Redis key for a user's fetch queue.
func queueKey(userID string) string {
	return fmt.Sprintf("%s:%s", redisQueuePrefix, userID)
}
