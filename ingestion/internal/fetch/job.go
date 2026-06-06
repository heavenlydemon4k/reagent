// Package fetch handles fetch job enqueuing and processing for the Ingestion Mesh.
// Jobs are pushed to per-user Redis queues and consumed by worker pools.
package fetch

import (
	"time"

	"github.com/google/uuid"
)

// FetchJob represents a single fetch work item enqueued from a webhook notification.
type FetchJob struct {
	ID         string     `json:"id"`                    // UUID
	UserID     string     `json:"user_id"`               // User UUID (as string)
	AccountID  string     `json:"account_id"`            // Connected account UUID (as string)
	Source     string     `json:"source"`                // "gmail" | "outlook"
	HistoryID  *string    `json:"history_id,omitempty"`  // Gmail history ID
	DeltaLink  *string    `json:"delta_link,omitempty"`  // Outlook delta link
	PageToken  *string    `json:"page_token,omitempty"`  // Pagination token
	EnqueuedAt time.Time  `json:"enqueued_at"`           // When the job was enqueued
	Attempts   int        `json:"attempts"`              // Number of processing attempts
}

// NewFetchJob creates a new FetchJob with a generated ID and current timestamp.
func NewFetchJob(userID, accountID, source string) *FetchJob {
	return &FetchJob{
		ID:         uuid.NewString(),
		UserID:     userID,
		AccountID:  accountID,
		Source:     source,
		EnqueuedAt: time.Now().UTC(),
		Attempts:   0,
	}
}

// NewGmailFetchJob creates a FetchJob specifically for Gmail history fetch.
func NewGmailFetchJob(userID, accountID string, historyID uint64) *FetchJob {
	hid := fmtUInt64(historyID)
	job := NewFetchJob(userID, accountID, "gmail")
	job.HistoryID = &hid
	return job
}

// NewOutlookFetchJob creates a FetchJob specifically for Outlook delta fetch.
func NewOutlookFetchJob(userID, accountID, deltaLink string) *FetchJob {
	job := NewFetchJob(userID, accountID, "outlook")
	job.DeltaLink = &deltaLink
	return job
}

// IncrementAttempts increments the attempt counter.
func (j *FetchJob) IncrementAttempts() {
	j.Attempts++
}

// fmtUInt64 formats a uint64 as a string without importing strconv in this file.
func fmtUInt64(v uint64) string {
	// Simple uint64 to string conversion
	if v == 0 {
		return "0"
	}
	var buf [20]byte // uint64 max is 20 digits
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
