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
// InterruptNotificationBuilder — Interrupt notification builder
// ============================================================================

// InterruptNotificationBuilder constructs high-priority interrupt notifications
// for urgent decision cards. These bypass quiet hours and use high-priority
// FCM / apns-priority 10 delivery channels.
type InterruptNotificationBuilder struct{}

// NewInterruptNotificationBuilder creates a new interrupt notification builder.
func NewInterruptNotificationBuilder() *InterruptNotificationBuilder {
	return &InterruptNotificationBuilder{}
}

// Build creates an interrupt notification for a single urgent decision card.
func (b *InterruptNotificationBuilder) Build(userID uuid.UUID, card models.DecisionCard) *models.Notification {
	senderName := extractSenderName(card.FromField)
	atomicAsk := card.NeedFromUser

	if atomicAsk == "" {
		atomicAsk = card.TheyWant
	}

	title := fmt.Sprintf("Urgent: %s", senderName)
	if len(title) > 60 {
		title = title[:57] + "..."
	}

	body := atomicAsk
	if len(body) > 120 {
		body = body[:117] + "..."
	}

	payload := models.InterruptNotificationPayload{
		CardID:       card.ID,
		SenderName:   senderName,
		AtomicAsk:    atomicAsk,
		UrgencyScore: card.UrgencyScore,
	}
	data, _ := json.Marshal(payload)

	return &models.Notification{
		ID:        uuid.New(),
		UserID:    userID,
		Type:      "interrupt",
		Title:     title,
		Body:      body,
		Data:      data,
		CreatedAt: time.Now().UTC(),
	}
}

// BuildFromPayload creates an interrupt notification from a pre-built payload.
func (b *InterruptNotificationBuilder) BuildFromPayload(userID uuid.UUID, payload models.InterruptNotificationPayload) *models.Notification {
	title := fmt.Sprintf("Urgent: %s", payload.SenderName)
	if len(title) > 60 {
		title = title[:57] + "..."
	}

	body := payload.AtomicAsk
	if len(body) > 120 {
		body = body[:117] + "..."
	}

	data, _ := json.Marshal(payload)

	return &models.Notification{
		ID:        uuid.New(),
		UserID:    userID,
		Type:      "interrupt",
		Title:     title,
		Body:      body,
		Data:      data,
		CreatedAt: time.Now().UTC(),
	}
}

// BuildReplyNeeded creates an interrupt for a card that needs a direct reply.
func (b *InterruptNotificationBuilder) BuildReplyNeeded(userID uuid.UUID, cardID uuid.UUID, senderName, question string, urgency float64) *models.Notification {
	title := fmt.Sprintf("Reply needed: %s", senderName)
	if len(title) > 60 {
		title = title[:57] + "..."
	}

	body := question
	if len(body) > 120 {
		body = body[:117] + "..."
	}

	payload := models.InterruptNotificationPayload{
		CardID:       cardID,
		SenderName:   senderName,
		AtomicAsk:    question,
		UrgencyScore: urgency,
	}
	data, _ := json.Marshal(payload)

	return &models.Notification{
		ID:        uuid.New(),
		UserID:    userID,
		Type:      "interrupt",
		Title:     title,
		Body:      body,
		Data:      data,
		CreatedAt: time.Now().UTC(),
	}
}

// Priority returns the interrupt notification priority (always highest).
func (b *InterruptNotificationBuilder) Priority() int {
	return 10
}

// ============================================================================
// HELPERS
// ============================================================================

// extractSenderName extracts the sender name from the FromField JSON.
func extractSenderName(fromField json.RawMessage) string {
	if len(fromField) == 0 {
		return "Unknown"
	}

	var parsed struct {
		Name    string `json:"name"`
		Email   string `json:"email"`
		Address string `json:"address"`
	}
	if err := json.Unmarshal(fromField, &parsed); err != nil {
		return "Unknown"
	}

	if parsed.Name != "" {
		return parsed.Name
	}
	if parsed.Email != "" {
		return parsed.Email
	}
	if parsed.Address != "" {
		return parsed.Address
	}
	return "Unknown"
}
