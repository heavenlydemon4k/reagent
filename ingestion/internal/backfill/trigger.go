package backfill

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Trigger enqueues a new backfill job after OAuth completion.
// This is called by the OAuth callback handler to kick off historical sync.
func Trigger(ctx context.Context, redisClient *redis.Client, userID, accountID uuid.UUID, provider, historyID string, log *slog.Logger) error {
	now := time.Now().UTC()
	startDate := now.Add(-BackfillDateRange)

	job := &BackfillJob{
		UserID:    userID,
		AccountID: accountID,
		Provider:  provider,
		HistoryID: historyID,
		StartDate: startDate,
		EndDate:   now,
		Status:    StatusPending,
		Progress:  0,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// For Outlook, we start with an empty deltaLink for full sync
	if provider == "outlook" {
		job.DeltaLink = ""
	}

	scheduler := NewScheduler(redisClient, log)
	if err := scheduler.Enqueue(ctx, job); err != nil {
		return fmt.Errorf("enqueue backfill job: %w", err)
	}

	log.Info("backfill triggered after OAuth",
		"user_id", userID,
		"account_id", accountID,
		"provider", provider,
		"start_date", startDate.Format("2006-01-02"),
	)

	return nil
}

// TriggerFromCallback is a convenience wrapper that extracts userID from context
// or uses a provided value. It is called directly from the OAuth callback handler.
func TriggerFromCallback(ctx context.Context, redisClient *redis.Client, userID, accountID uuid.UUID, provider, historyID string, log *slog.Logger) {
	if err := Trigger(ctx, redisClient, userID, accountID, provider, historyID, log); err != nil {
		// Log but don't fail the OAuth flow — backfill is best-effort
		log.Error("failed to trigger backfill, user will have empty decision queue",
			"user_id", userID,
			"account_id", accountID,
			"error", err,
		)
	}
}
