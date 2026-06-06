package backfill

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Scheduler manages the Redis-backed job queue for backfill operations.
// It handles enqueue, dequeue, progress tracking, and rate limiting.
type Scheduler struct {
	redis *redis.Client
	log   *slog.Logger
}

// NewScheduler creates a new Scheduler backed by Redis.
func NewScheduler(redisClient *redis.Client, log *slog.Logger) *Scheduler {
	return &Scheduler{
		redis: redisClient,
		log:   log.With("component", "backfill_scheduler"),
	}
}

// ---------------------------------------------------------------------------
// Job lifecycle
// ---------------------------------------------------------------------------

// Enqueue pushes a backfill job onto the Redis queue and stores its details.
// Called by the OAuth callback handler after successful token exchange.
func (s *Scheduler) Enqueue(ctx context.Context, job *BackfillJob) error {
	if err := job.Validate(); err != nil {
		return fmt.Errorf("validate job: %w", err)
	}

	job.Status = StatusPending
	job.CreatedAt = time.Now().UTC()
	job.UpdatedAt = job.CreatedAt

	data, err := job.ToJSON()
	if err != nil {
		return fmt.Errorf("serialize job: %w", err)
	}

	pipe := s.redis.Pipeline()
	// Push job onto the queue (left push, so BLPOP on the right processes FIFO)
	lpush := pipe.LPush(ctx, QueueKey, data)
	// Store job details in a hash for fast lookup
	pipe.HSet(ctx, JobKey(job.UserID, job.AccountID),
		"status", string(job.Status),
		"progress", strconv.Itoa(job.Progress),
		"emails_found", strconv.Itoa(job.EmailsFound),
		"emails_processed", strconv.Itoa(job.EmailsProcessed),
		"created_at", job.CreatedAt.Format(time.RFC3339),
		"updated_at", job.UpdatedAt.Format(time.RFC3339),
	)

	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("enqueue job: %w", err)
	}

	if err := lpush.Err(); err != nil {
		return fmt.Errorf("lpush job: %w", err)
	}

	s.log.Info("backfill job enqueued",
		"user_id", job.UserID,
		"account_id", job.AccountID,
		"provider", job.Provider,
	)
	return nil
}

// Dequeue blocks until a job is available or the context is cancelled.
// It uses BRPOP for reliable FIFO consumption.
func (s *Scheduler) Dequeue(ctx context.Context) (*BackfillJob, error) {
	result, err := s.redis.BRPop(ctx, 0, QueueKey).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, context.Canceled
		}
		return nil, fmt.Errorf("brpop: %w", err)
	}

	if len(result) < 2 {
		return nil, fmt.Errorf("invalid brpop result")
	}

	job, err := BackfillJobFromJSON([]byte(result[1]))
	if err != nil {
		return nil, fmt.Errorf("deserialize job: %w", err)
	}

	// Update status to running
	job.Status = StatusRunning
	job.UpdatedAt = time.Now().UTC()
	if err := s.UpdateJobStatus(ctx, job); err != nil {
		s.log.Warn("failed to update job status to running", "error", err)
	}

	s.log.Info("backfill job dequeued",
		"user_id", job.UserID,
		"account_id", job.AccountID,
		"provider", job.Provider,
	)
	return job, nil
}

// ---------------------------------------------------------------------------
// Progress tracking
// ---------------------------------------------------------------------------

// UpdateProgress writes the current progress to Redis.
// Called by the worker after every ProgressUpdateInterval emails.
func (s *Scheduler) UpdateProgress(ctx context.Context, job *BackfillJob) error {
	job.UpdatedAt = time.Now().UTC()

	pipe := s.redis.Pipeline()
	pipe.HSet(ctx, ProgressHashKey(job.UserID),
		"status", string(job.Status),
		"progress", strconv.Itoa(job.Progress),
		"emails_found", strconv.Itoa(job.EmailsFound),
		"emails_processed", strconv.Itoa(job.EmailsProcessed),
		"emails_skipped", strconv.Itoa(job.EmailsSkipped),
		"emails_failed", strconv.Itoa(job.EmailsFailed),
		"updated_at", job.UpdatedAt.Format(time.RFC3339),
	)
	pipe.HSet(ctx, JobKey(job.UserID, job.AccountID),
		"status", string(job.Status),
		"progress", strconv.Itoa(job.Progress),
		"emails_processed", strconv.Itoa(job.EmailsProcessed),
		"updated_at", job.UpdatedAt.Format(time.RFC3339),
	)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("update progress: %w", err)
	}
	return nil
}

// GetProgress retrieves the current progress snapshot from Redis.
// Called by the status API endpoint.
func (s *Scheduler) GetProgress(ctx context.Context, userID uuid.UUID) (*ProgressSnapshot, error) {
	data, err := s.redis.HGetAll(ctx, ProgressHashKey(userID)).Result()
	if err != nil {
		return nil, fmt.Errorf("hgetall progress: %w", err)
	}

	// If no progress data exists, check if there's a pending job
	if len(data) == 0 {
		// Check job key to see if a job exists at all
		// We need account_id to form the job key, so iterate
		pattern := fmt.Sprintf("%s:%s:*", jobKeyPrefix, userID.String())
		keys, err := s.redis.Keys(ctx, pattern).Result()
		if err != nil || len(keys) == 0 {
			return nil, fmt.Errorf("no backfill job found for user %s", userID)
		}
		// Return a minimal snapshot indicating the job hasn't started yet
		return &ProgressSnapshot{
			Status: string(StatusPending),
		}, nil
	}

	snap := &ProgressSnapshot{
		Status:   data["status"],
		LastError: data["last_error"],
	}

	if v, ok := data["progress"]; ok {
		snap.Progress, _ = strconv.Atoi(v)
	}
	if v, ok := data["emails_found"]; ok {
		snap.EmailsFound, _ = strconv.Atoi(v)
	}
	if v, ok := data["emails_processed"]; ok {
		snap.EmailsProcessed, _ = strconv.Atoi(v)
	}
	if v, ok := data["emails_skipped"]; ok {
		snap.EmailsSkipped, _ = strconv.Atoi(v)
	}
	if v, ok := data["emails_failed"]; ok {
		snap.EmailsFailed, _ = strconv.Atoi(v)
	}
	if v, ok := data["retry_count"]; ok {
		snap.RetryCount, _ = strconv.Atoi(v)
	}
	if v, ok := data["updated_at"]; ok {
		snap.StartedAt, _ = time.Parse(time.RFC3339, v)
	}
	if v, ok := data["completed_at"]; ok && v != "" {
		t, _ := time.Parse(time.RFC3339, v)
		snap.CompletedAt = &t
	}

	return snap, nil
}

// ---------------------------------------------------------------------------
// Rate limiting
// ---------------------------------------------------------------------------

// CanProcessEmail checks if the user has remaining quota for the current hour.
// It uses a Redis counter with a 1-hour TTL.
func (s *Scheduler) CanProcessEmail(ctx context.Context, userID uuid.UUID) (bool, error) {
	key := CountKey(userID)

	// Use INCR to atomically increment and get the new value
	val, err := s.redis.Incr(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("incr rate limit counter: %w", err)
	}

	// Set TTL on first increment (when key is created)
	if val == 1 {
		s.redis.Expire(ctx, key, RateLimitWindow)
	}

	if int(val) > RateLimitMaxEmailsPerHour {
		return false, nil
	}

	return true, nil
}

// GetRateLimitRemaining returns how many emails can still be processed this hour.
func (s *Scheduler) GetRateLimitRemaining(ctx context.Context, userID uuid.UUID) (int, error) {
	key := CountKey(userID)
	val, err := s.redis.Get(ctx, key).Result()
	if err == redis.Nil {
		return RateLimitMaxEmailsPerHour, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get rate limit counter: %w", err)
	}

	count, _ := strconv.Atoi(val)
	remaining := RateLimitMaxEmailsPerHour - count
	if remaining < 0 {
		remaining = 0
	}
	return remaining, nil
}

// ---------------------------------------------------------------------------
// Job status management
// ---------------------------------------------------------------------------

// MarkComplete marks the job as completed and cleans up Redis keys.
func (s *Scheduler) MarkComplete(ctx context.Context, job *BackfillJob) error {
	job.Status = StatusComplete
	job.Progress = 100
	now := time.Now().UTC()
	job.UpdatedAt = now
	job.CompletedAt = &now

	pipe := s.redis.Pipeline()
	pipe.HSet(ctx, ProgressHashKey(job.UserID),
		"status", string(job.Status),
		"progress", "100",
		"completed_at", now.Format(time.RFC3339),
	)
	pipe.HSet(ctx, JobKey(job.UserID, job.AccountID),
		"status", string(job.Status),
		"progress", "100",
		"completed_at", now.Format(time.RFC3339),
	)
	// Clean up the rate limit counter
	pipe.Del(ctx, CountKey(job.UserID))

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("mark complete: %w", err)
	}

	s.log.Info("backfill job completed",
		"user_id", job.UserID,
		"account_id", job.AccountID,
		"emails_processed", job.EmailsProcessed,
		"emails_found", job.EmailsFound,
	)
	return nil
}

// MarkFailed marks the job as failed after exhausting retries.
func (s *Scheduler) MarkFailed(ctx context.Context, job *BackfillJob, reason string) error {
	job.Status = StatusFailed
	job.LastError = reason
	job.UpdatedAt = time.Now().UTC()

	pipe := s.redis.Pipeline()
	pipe.HSet(ctx, ProgressHashKey(job.UserID),
		"status", string(job.Status),
		"last_error", reason,
		"retry_count", strconv.Itoa(job.RetryCount),
		"updated_at", job.UpdatedAt.Format(time.RFC3339),
	)
	pipe.HSet(ctx, JobKey(job.UserID, job.AccountID),
		"status", string(job.Status),
		"last_error", reason,
		"updated_at", job.UpdatedAt.Format(time.RFC3339),
	)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}

	s.log.Error("backfill job failed",
		"user_id", job.UserID,
		"account_id", job.AccountID,
		"retries", job.RetryCount,
		"reason", reason,
	)
	return nil
}

// UpdateJobStatus updates the status fields in Redis.
func (s *Scheduler) UpdateJobStatus(ctx context.Context, job *BackfillJob) error {
	job.UpdatedAt = time.Now().UTC()

	pipe := s.redis.Pipeline()
	pipe.HSet(ctx, JobKey(job.UserID, job.AccountID),
		"status", string(job.Status),
		"updated_at", job.UpdatedAt.Format(time.RFC3339),
	)
	if job.LastError != "" {
		pipe.HSet(ctx, JobKey(job.UserID, job.AccountID), "last_error", job.LastError)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("update job status: %w", err)
	}
	return nil
}

// Cleanup removes all Redis keys associated with a backfill job.
// Called after successful completion or explicit cancellation.
func (s *Scheduler) Cleanup(ctx context.Context, userID, accountID uuid.UUID) error {
	pipe := s.redis.Pipeline()
	pipe.Del(ctx, ProgressHashKey(userID))
	pipe.Del(ctx, CountKey(userID))
	pipe.Del(ctx, JobKey(userID, accountID))

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("cleanup: %w", err)
	}

	s.log.Debug("backfill cleanup complete", "user_id", userID, "account_id", accountID)
	return nil
}

// ---------------------------------------------------------------------------
// Helper: Re-enqueue for retry
// ---------------------------------------------------------------------------

// RequeueForRetry re-enqueues a failed job for retry with exponential backoff.
// The job is pushed to the front of the queue so it gets picked up quickly.
func (s *Scheduler) RequeueForRetry(ctx context.Context, job *BackfillJob) error {
	job.RetryCount++
	job.Status = StatusPending
	job.UpdatedAt = time.Now().UTC()

	data, err := job.ToJSON()
	if err != nil {
		return fmt.Errorf("serialize job for retry: %w", err)
	}

	// Use LPush so the retry is processed next (LIFO for retries)
	if err := s.redis.LPush(ctx, QueueKey, data).Err(); err != nil {
		return fmt.Errorf("requeue for retry: %w", err)
	}

	s.log.Info("backfill job requeued for retry",
		"user_id", job.UserID,
		"account_id", job.AccountID,
		"retry", job.RetryCount,
	)
	return nil
}
