// Package notify handles push notification dispatch via FCM, APNS, and WebSocket.
package notify

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
)

// ============================================================================
// BatchNotificationBuilder — Batch notification builder
// ============================================================================

// BatchNotificationBuilder constructs batch notifications that summarize
// multiple pending decision cards. Batch notifications are lower priority
// and respect quiet hours.
type BatchNotificationBuilder struct{}

// NewBatchNotificationBuilder creates a new batch notification builder.
func NewBatchNotificationBuilder() *BatchNotificationBuilder {
	return &BatchNotificationBuilder{}
}

// Build creates a batch notification for a user's pending card queue.
func (b *BatchNotificationBuilder) Build(userID uuid.UUID, batchInfo models.BatchInfo) *models.Notification {
	title := fmt.Sprintf("%d decisions waiting", batchInfo.Size)
	if batchInfo.Size == 1 {
		title = "1 decision waiting"
	}

	var body string
	if batchInfo.EstimatedClearTimeMinutes > 0 {
		body = fmt.Sprintf("About %d min to clear your queue", batchInfo.EstimatedClearTimeMinutes)
	} else {
		body = "Tap to review and respond"
	}

	payload := models.BatchNotificationPayload{
		BatchSize:                 batchInfo.Size,
		EstimatedClearTimeMinutes: batchInfo.EstimatedClearTimeMinutes,
	}
	data, _ := json.Marshal(payload)

	return &models.Notification{
		ID:        uuid.New(),
		UserID:    userID,
		Type:      "batch",
		Title:     title,
		Body:      body,
		Data:      data,
		CreatedAt: time.Now().UTC(),
	}
}

// BuildEmpty creates a notification for when the user's queue is empty
// (e.g., after processing all cards).
func (b *BatchNotificationBuilder) BuildEmpty(userID uuid.UUID) *models.Notification {
	return &models.Notification{
		ID:        uuid.New(),
		UserID:    userID,
		Type:      "batch",
		Title:     "All caught up!",
		Body:      "No pending decisions in your queue.",
		Data:      json.RawMessage(`{"batch_size":0,"queue_empty":true}`),
		CreatedAt: time.Now().UTC(),
	}
}

// BuildDigest creates a daily digest-style batch notification with
// detailed card summaries.
func (b *BatchNotificationBuilder) BuildDigest(userID uuid.UUID, cards []models.DecisionCard) *models.Notification {
	urgentCount := 0
	for _, c := range cards {
		if c.UrgencyScore >= 0.7 {
			urgentCount++
		}
	}

	title := fmt.Sprintf("Daily digest: %d decisions", len(cards))
	body := fmt.Sprintf("%d urgent items need your attention", urgentCount)
	if urgentCount == 0 {
		body = "All items are normal priority"
	}

	digestData := map[string]interface{}{
		"total_count":  len(cards),
		"urgent_count": urgentCount,
		"digest_type":  "daily",
	}
	data, _ := json.Marshal(digestData)

	return &models.Notification{
		ID:        uuid.New(),
		UserID:    userID,
		Type:      "batch",
		Title:     title,
		Body:      body,
		Data:      data,
		CreatedAt: time.Now().UTC(),
	}
}
