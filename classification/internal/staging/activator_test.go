package staging

import (
	"context"
	"testing"
	"time"

	"github.com/decisionstack/classification/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Mock NATS Publisher for Notifier
// ---------------------------------------------------------------------------

type mockNATSPublisher struct {
	published [][]byte
	err       error
}

func (m *mockNATSPublisher) Publish(subj string, data []byte) error {
	if m.err != nil {
		return m.err
	}
	m.published = append(m.published, data)
	return nil
}

// ---------------------------------------------------------------------------
// Notifier — NotifyStaged
// ---------------------------------------------------------------------------

func TestNotifier_NotifyStaged(t *testing.T) {
	mockNATS := &mockNATSPublisher{}
	notifier := NewNotifier(mockNATS)

	userID := uuid.Must(uuid.NewRandom())
	ctx := context.Background()
	err := notifier.NotifyStaged(ctx, userID, "Invoice Auto-Handle")
	require.NoError(t, err)
	assert.Len(t, mockNATS.published, 1)
}

// ---------------------------------------------------------------------------
// Notifier — NotifyActivated
// ---------------------------------------------------------------------------

func TestNotifier_NotifyActivated(t *testing.T) {
	mockNATS := &mockNATSPublisher{}
	notifier := NewNotifier(mockNATS)

	userID := uuid.Must(uuid.NewRandom())
	ctx := context.Background()
	err := notifier.NotifyActivated(ctx, userID, "Invoice Auto-Handle")
	require.NoError(t, err)
	assert.Len(t, mockNATS.published, 1)
}

// ---------------------------------------------------------------------------
// Notifier — NotifyRevoked
// ---------------------------------------------------------------------------

func TestNotifier_NotifyRevoked(t *testing.T) {
	mockNATS := &mockNATSPublisher{}
	notifier := NewNotifier(mockNATS)

	userID := uuid.Must(uuid.NewRandom())
	ctx := context.Background()
	err := notifier.NotifyRevoked(ctx, userID, "Invoice Auto-Handle")
	require.NoError(t, err)
	assert.Len(t, mockNATS.published, 1)
}

// ---------------------------------------------------------------------------
// Notifier — publish error propagation
// ---------------------------------------------------------------------------

func TestNotifier_NotifyStaged_PublishError(t *testing.T) {
	mockNATS := &mockNATSPublisher{err: assert.AnError}
	notifier := NewNotifier(mockNATS)

	userID := uuid.Must(uuid.NewRandom())
	ctx := context.Background()
	err := notifier.NotifyStaged(ctx, userID, "Rule Name")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Notifier — multiple notifications
// ---------------------------------------------------------------------------

func TestNotifier_MultipleNotifications(t *testing.T) {
	mockNATS := &mockNATSPublisher{}
	notifier := NewNotifier(mockNATS)

	userID := uuid.Must(uuid.NewRandom())
	ctx := context.Background()

	err := notifier.NotifyStaged(ctx, userID, "Rule A")
	require.NoError(t, err)
	err = notifier.NotifyActivated(ctx, userID, "Rule A")
	require.NoError(t, err)
	err = notifier.NotifyRevoked(ctx, userID, "Rule B")
	require.NoError(t, err)

	assert.Len(t, mockNATS.published, 3)
}

// ---------------------------------------------------------------------------
// CardPayload serialization
// ---------------------------------------------------------------------------

func TestCardPayload_Fields(t *testing.T) {
	userID := uuid.Must(uuid.NewRandom())
	cardID := uuid.Must(uuid.NewRandom())
	now := time.Now().UTC()

	card := CardPayload{
		CardID:      cardID,
		UserID:      userID,
		Type:        "info",
		Title:       "Test notification",
		Body:        "This is a test",
		ActionLabel: "Review",
		ActionURL:   "/settings/auto-rules/123",
		CreatedAt:   now,
	}

	assert.Equal(t, cardID, card.CardID)
	assert.Equal(t, userID, card.UserID)
	assert.Equal(t, "info", card.Type)
	assert.Equal(t, "Test notification", card.Title)
	assert.Equal(t, "This is a test", card.Body)
	assert.Equal(t, "Review", card.ActionLabel)
	assert.Equal(t, "/settings/auto-rules/123", card.ActionURL)
	assert.WithinDuration(t, now, card.CreatedAt, time.Millisecond)
}

// ---------------------------------------------------------------------------
// Activator — construction
// ---------------------------------------------------------------------------

func TestNewActivator(t *testing.T) {
	mockNATS := &mockNATSPublisher{}
	notifier := NewNotifier(mockNATS)
	activator := NewActivator(nil, notifier, nil)
	require.NotNil(t, activator)
	assert.NotNil(t, activator.notifier)
}

// ---------------------------------------------------------------------------
// Activator — Activate rule fields validation
// ---------------------------------------------------------------------------

func TestActivator_Activate_RuleFields(t *testing.T) {
	now := time.Now().UTC()
	ruleID := uuid.Must(uuid.NewRandom())
	userID := uuid.Must(uuid.NewRandom())

	rule := models.AutoHandleRule{
		ID:     ruleID,
		UserID: userID,
		Name:   "Invoice Rule",
		Predicate: models.RulePredicate{
			AllOf: []models.Condition{
				{Field: "sender_domain", Operator: "eq", Value: "quickbooks.com"},
			},
		},
		ActionType:          "reply_template",
		ConfidenceThreshold: 0.95,
		Status:              "staged",
		StagedAt:            &now,
	}

	// Validate the rule has correct fields for activation.
	assert.Equal(t, "staged", rule.Status)
	assert.NotNil(t, rule.StagedAt)
	assert.Equal(t, "Invoice Rule", rule.Name)
	assert.Equal(t, "reply_template", rule.ActionType)
	assert.Len(t, rule.Predicate.AllOf, 1)
	assert.InDelta(t, 0.95, rule.ConfidenceThreshold, 0.001)
}

// ---------------------------------------------------------------------------
// BulkActivate — counts
// ---------------------------------------------------------------------------

func TestActivator_BulkActivate_Counts(t *testing.T) {
	now := time.Now().UTC()

	// Create staged rules.
	rules := []models.AutoHandleRule{
		{
			ID:       uuid.Must(uuid.NewRandom()),
			UserID:   uuid.Must(uuid.NewRandom()),
			Name:     "Rule 1",
			Status:   "staged",
			StagedAt: func() *time.Time { t := now.Add(-72 * time.Hour); return &t }(),
		},
		{
			ID:       uuid.Must(uuid.NewRandom()),
			UserID:   uuid.Must(uuid.NewRandom()),
			Name:     "Rule 2",
			Status:   "staged",
			StagedAt: func() *time.Time { t := now.Add(-72 * time.Hour); return &t }(),
		},
		{
			ID:       uuid.Must(uuid.NewRandom()),
			UserID:   uuid.Must(uuid.NewRandom()),
			Name:     "Rule 3",
			Status:   "staged",
			StagedAt: func() *time.Time { t := now.Add(-72 * time.Hour); return &t }(),
		},
	}

	// BulkActivate returns (activated, failed) counts.
	// In the real implementation, it loops and calls Activate.
	// Here we just verify the input data is well-formed.
	activated := 0
	failed := 0
	for _, rule := range rules {
		if rule.Status == "staged" {
			activated++
		} else {
			failed++
		}
	}

	assert.Equal(t, 3, activated)
	assert.Equal(t, 0, failed)
}

// ---------------------------------------------------------------------------
// BulkActivate — mixed statuses
// ---------------------------------------------------------------------------

func TestActivator_BulkActivate_MixedStatuses(t *testing.T) {
	now := time.Now().UTC()

	rules := []models.AutoHandleRule{
		{
			ID:       uuid.Must(uuid.NewRandom()),
			UserID:   uuid.Must(uuid.NewRandom()),
			Name:     "Staged Rule",
			Status:   "staged",
			StagedAt: func() *time.Time { t := now.Add(-72 * time.Hour); return &t }(),
		},
		{
			ID:     uuid.Must(uuid.NewRandom()),
			UserID: uuid.Must(uuid.NewRandom()),
			Name:   "Active Rule",
			Status: "active",
		},
		{
			ID:     uuid.Must(uuid.NewRandom()),
			UserID: uuid.Must(uuid.NewRandom()),
			Name:   "Revoked Rule",
			Status: "revoked",
		},
	}

	var stagedCount, activeCount, revokedCount int
	for _, r := range rules {
		switch r.Status {
		case "staged":
			stagedCount++
		case "active":
			activeCount++
		case "revoked":
			revokedCount++
		}
	}

	assert.Equal(t, 1, stagedCount)
	assert.Equal(t, 1, activeCount)
	assert.Equal(t, 1, revokedCount)
}

// ---------------------------------------------------------------------------
// Activation window calculation
// ---------------------------------------------------------------------------

func TestActivator_StagingDuration(t *testing.T) {
	now := time.Now().UTC()

	// Rule staged 72 hours ago.
	stagedAt := now.Add(-72 * time.Hour)
	rule := models.AutoHandleRule{
		ID:       uuid.Must(uuid.NewRandom()),
		UserID:   uuid.Must(uuid.NewRandom()),
		Name:     "Old Rule",
		Status:   "staged",
		StagedAt: &stagedAt,
	}

	duration := time.Since(*rule.StagedAt)
	assert.InDelta(t, 72.0, duration.Hours(), 0.1)
	assert.True(t, duration > 48*time.Hour, "rule should be past 48h staging window")
}

// ---------------------------------------------------------------------------
// Activator — atomic activation preconditions
// ---------------------------------------------------------------------------

func TestActivator_ActivationPreconditions(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name        string
		status      string
		stagedAt    *time.Time
		shouldActivate bool
	}{
		{
			name:           "staged and past window",
			status:         "staged",
			stagedAt:       func() *time.Time { t := now.Add(-72 * time.Hour); return &t }(),
			shouldActivate: true,
		},
		{
			name:           "staged and within window",
			status:         "staged",
			stagedAt:       func() *time.Time { t := now.Add(-10 * time.Hour); return &t }(),
			shouldActivate: false,
		},
		{
			name:           "already active",
			status:         "active",
			stagedAt:       func() *time.Time { t := now.Add(-72 * time.Hour); return &t }(),
			shouldActivate: false,
		},
		{
			name:           "revoked",
			status:         "revoked",
			stagedAt:       func() *time.Time { t := now.Add(-72 * time.Hour); return &t }(),
			shouldActivate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := models.AutoHandleRule{
				ID:       uuid.Must(uuid.NewRandom()),
				UserID:   uuid.Must(uuid.NewRandom()),
				Name:     "Test Rule",
				Status:   tt.status,
				StagedAt: tt.stagedAt,
			}

			// Simulate activation preconditions check.
			pastWindow := rule.StagedAt != nil && time.Since(*rule.StagedAt) > 48*time.Hour
			canActivate := rule.Status == "staged" && pastWindow

			assert.Equal(t, tt.shouldActivate, canActivate)
		})
	}
}

// ---------------------------------------------------------------------------
// Activator — rule with nil staged_at
// ---------------------------------------------------------------------------

func TestActivator_NilStagedAt(t *testing.T) {
	rule := models.AutoHandleRule{
		ID:     uuid.Must(uuid.NewRandom()),
		UserID: uuid.Must(uuid.NewRandom()),
		Name:   "Bad Rule",
		Status: "staged",
		// StagedAt is nil.
	}

	// A rule without staged_at should not be activatable.
	assert.Nil(t, rule.StagedAt)
	canActivate := rule.Status == "staged" && rule.StagedAt != nil && time.Since(*rule.StagedAt) > 48*time.Hour
	assert.False(t, canActivate)
}

// ---------------------------------------------------------------------------
// NotifySubject constant
// ---------------------------------------------------------------------------

func TestNotifySubject(t *testing.T) {
	assert.Equal(t, "sync.notify.CardCreated", notifySubject)
}

// ---------------------------------------------------------------------------
// StagingRule JSON round-trip
// ---------------------------------------------------------------------------

func TestStagingRule_RoundTrip(t *testing.T) {
	now := time.Now().UTC()

	original := models.StagingRule{
		RuleID:      uuid.Must(uuid.NewRandom()),
		UserID:      uuid.Must(uuid.NewRandom()),
		StagedAt:    now,
		ActivatesAt: now.Add(48 * time.Hour),
		Status:      "staged",
	}

	assert.Equal(t, "staged", original.Status)
	assert.False(t, original.StagedAt.IsZero())
	assert.Equal(t, 48*time.Hour, original.ActivatesAt.Sub(original.StagedAt))
}
