// Package backfill provides the historical email backfill pipeline. It runs
// as a separate worker binary to avoid interfering with real-time ingestion.
// After OAuth completion, the backfill processes the last 90 days of email
// history, rate-limited to 100 emails/hour/user.
package backfill

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// JobStatus represents the lifecycle state of a backfill job.
type JobStatus string

const (
	StatusPending   JobStatus = "pending"
	StatusRunning   JobStatus = "running"
	StatusComplete  JobStatus = "complete"
	StatusFailed    JobStatus = "failed"
	StatusCancelled JobStatus = "cancelled"
)

// BackfillJob represents a single backfill request. It is enqueued to Redis
// after OAuth completion and picked up by the backfill worker.
type BackfillJob struct {
	UserID          uuid.UUID `json:"user_id" redis:"user_id"`
	AccountID       uuid.UUID `json:"account_id" redis:"account_id"`
	Provider        string    `json:"provider" redis:"provider"`                   // "gmail" | "outlook"
	HistoryID       string    `json:"history_id,omitempty" redis:"history_id"`     // starting historyId (from OAuth callback)
	DeltaLink       string    `json:"delta_link,omitempty" redis:"delta_link"`     // for Outlook
	StartDate       time.Time `json:"start_date" redis:"start_date"`               // 90 days ago
	EndDate         time.Time `json:"end_date" redis:"end_date"`                   // now
	Status          JobStatus `json:"status" redis:"status"`                       // "pending" | "running" | "complete" | "failed"
	Progress        int       `json:"progress" redis:"progress"`                   // 0-100
	EmailsFound     int       `json:"emails_found" redis:"emails_found"`           // total discovered
	EmailsProcessed int       `json:"emails_processed" redis:"emails_processed"`   // successfully ingested
	EmailsSkipped   int       `json:"emails_skipped" redis:"emails_skipped"`       // duplicates
	EmailsFailed    int       `json:"emails_failed" redis:"emails_failed"`         // processing errors
	RetryCount      int       `json:"retry_count" redis:"retry_count"`             // how many times retried
	LastError       string    `json:"last_error,omitempty" redis:"last_error"`     // last error message
	CreatedAt       time.Time `json:"created_at" redis:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" redis:"updated_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty" redis:"completed_at"`
}

// Validate returns an error if the job is invalid.
func (j *BackfillJob) Validate() error {
	if j.UserID == uuid.Nil {
		return fmt.Errorf("user_id is required")
	}
	if j.AccountID == uuid.Nil {
		return fmt.Errorf("account_id is required")
	}
	if j.Provider != "gmail" && j.Provider != "outlook" {
		return fmt.Errorf("provider must be 'gmail' or 'outlook', got %q", j.Provider)
	}
	if j.StartDate.IsZero() || j.EndDate.IsZero() {
		return fmt.Errorf("start_date and end_date are required")
	}
	if j.EndDate.Before(j.StartDate) {
		return fmt.Errorf("end_date must be after start_date")
	}
	return nil
}

// IsComplete returns true if the job has reached a terminal state.
func (j *BackfillJob) IsComplete() bool {
	return j.Status == StatusComplete || j.Status == StatusFailed || j.Status == StatusCancelled
}

// ToJSON serializes the job to JSON for Redis storage.
func (j *BackfillJob) ToJSON() ([]byte, error) {
	return json.Marshal(j)
}

// BackfillJobFromJSON deserializes a BackfillJob from JSON.
func BackfillJobFromJSON(data []byte) (*BackfillJob, error) {
	var job BackfillJob
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, fmt.Errorf("unmarshal backfill job: %w", err)
	}
	return &job, nil
}

// ProgressSnapshot is the DTO returned by the status API endpoint.
type ProgressSnapshot struct {
	Status          string    `json:"status"`
	Progress        int       `json:"progress"`
	EmailsFound     int       `json:"emails_found"`
	EmailsProcessed int       `json:"emails_processed"`
	EmailsSkipped   int       `json:"emails_skipped"`
	EmailsFailed    int       `json:"emails_failed"`
	RetryCount      int       `json:"retry_count"`
	LastError       string    `json:"last_error,omitempty"`
	StartedAt       time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}

// Redis key patterns.
const (
	// QueueKey is the Redis list key for pending backfill jobs.
	QueueKey = "backfill:queue"

	// progressKeyPrefix is the Redis hash key prefix for per-user progress.
	// Full key: backfill:progress:{user_id}
	progressKeyPrefix = "backfill:progress"

	// countKeyPrefix is the Redis counter key prefix for per-user rate limiting.
	// Full key: backfill:count:{user_id}
	countKeyPrefix = "backfill:count"

	// jobKeyPrefix is the Redis hash key prefix for storing job details.
	// Full key: backfill:job:{user_id}:{account_id}
	jobKeyPrefix = "backfill:job"
)

// RedisHashKey returns the Redis key for the progress hash.
func ProgressHashKey(userID uuid.UUID) string {
	return fmt.Sprintf("%s:%s", progressKeyPrefix, userID.String())
}

// CountKey returns the Redis key for the rate-limit counter.
func CountKey(userID uuid.UUID) string {
	return fmt.Sprintf("%s:%s", countKeyPrefix, userID.String())
}

// JobKey returns the Redis key for the job hash.
func JobKey(userID, accountID uuid.UUID) string {
	return fmt.Sprintf("%s:%s:%s", jobKeyPrefix, userID.String(), accountID.String())
}

// MaxRetries is the maximum number of retry attempts before marking a job failed.
const MaxRetries = 3

// RateLimitMaxEmailsPerHour is the maximum emails processed per user per hour.
const RateLimitMaxEmailsPerHour = 100

// RateLimitWindow is the TTL for the rate limit counter (1 hour).
const RateLimitWindow = time.Hour

// BatchSize is the number of emails fetched/persisted in a single batch.
const BatchSize = 20

// ProgressUpdateInterval is the number of emails between progress updates.
const ProgressUpdateInterval = 10

// BackfillDateRange is the default lookback window for historical backfill.
const BackfillDateRange = 90 * 24 * time.Hour // 90 days
