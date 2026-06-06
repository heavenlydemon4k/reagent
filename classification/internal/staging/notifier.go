// Package staging manages the 48-hour trust-building window for auto-handle rules.
package staging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// NATS publisher abstraction
// ---------------------------------------------------------------------------

// Publisher is the subset of NATS needed for notifications.
type Publisher interface {
	Publish(subj string, data []byte) error
}

// ---------------------------------------------------------------------------
// Notification payloads
// ---------------------------------------------------------------------------

// CardPayload is the shape of the message published to sync.notify.CardCreated.
type CardPayload struct {
	CardID      uuid.UUID `json:"card_id"`
	UserID      uuid.UUID `json:"user_id"`
	Type        string    `json:"type"`        // "info"
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	ActionLabel string    `json:"action_label,omitempty"`
	ActionURL   string    `json:"action_url,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// ---------------------------------------------------------------------------
// Notifier
// ---------------------------------------------------------------------------

// Notifier sends user-facing notifications via NATS.
type Notifier struct {
	nats Publisher
}

// NewNotifier creates a Notifier.
func NewNotifier(nats Publisher) *Notifier {
	return &Notifier{nats: nats}
}

// ---------------------------------------------------------------------------
// Notification methods
// ---------------------------------------------------------------------------

// NotifyStaged informs the user that a newly discovered rule is under 48-hour review.
func (n *Notifier) NotifyStaged(ctx context.Context, userID uuid.UUID, ruleName string) error {
	card := CardPayload{
		CardID:    uuid.New(),
		UserID:    userID,
		Type:      "info",
		Title:     "New auto-handle rule staged",
		Body:      fmt.Sprintf("I found a pattern ('%s') and will handle it automatically after a 48-hour review period.", ruleName),
		CreatedAt: time.Now().UTC(),
	}

	if err := n.publish(card); err != nil {
		return fmt.Errorf("notify staged: %w", err)
	}
	return nil
}

// NotifyActivated informs the user that a staged rule is now active.
func (n *Notifier) NotifyActivated(ctx context.Context, userID uuid.UUID, ruleName string) error {
	card := CardPayload{
		CardID:      uuid.New(),
		UserID:      userID,
		Type:        "info",
		Title:       "Auto-handle rule activated",
		Body:        fmt.Sprintf("Rule '%s' is now active. I'll handle these emails automatically.", ruleName),
		ActionLabel: "Review rule",
		ActionURL:   fmt.Sprintf("/settings/auto-rules/%s", uuid.New()), // placeholder URL pattern
		CreatedAt:   time.Now().UTC(),
	}

	if err := n.publish(card); err != nil {
		return fmt.Errorf("notify activated: %w", err)
	}
	return nil
}

// NotifyRevoked informs the user that an active rule has been turned off.
func (n *Notifier) NotifyRevoked(ctx context.Context, userID uuid.UUID, ruleName string) error {
	card := CardPayload{
		CardID:    uuid.New(),
		UserID:    userID,
		Type:      "info",
		Title:     "Auto-handle rule revoked",
		Body:      fmt.Sprintf("Rule '%s' has been turned off. Future matching emails will be sent to your Decision Stack for manual review.", ruleName),
		CreatedAt: time.Now().UTC(),
	}

	if err := n.publish(card); err != nil {
		return fmt.Errorf("notify revoked: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

const notifySubject = "sync.notify.CardCreated"

func (n *Notifier) publish(card CardPayload) error {
	data, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("marshal card payload: %w", err)
	}

	if err := n.nats.Publish(notifySubject, data); err != nil {
		return fmt.Errorf("publish to %s: %w", notifySubject, err)
	}

	return nil
}
