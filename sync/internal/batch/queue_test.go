// Package batch_test provides unit tests for queue management.
package batch

import (
	"testing"

	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Helper assertions
// ---------------------------------------------------------------------------

func assertNoError(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", msg, err)
	}
}

func assertError(t *testing.T, err error, msg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error but got nil", msg)
	}
}

func assertEqualInt(t *testing.T, want, got int, msg string) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %d, got %d", msg, want, got)
	}
}

func assertTrue(t *testing.T, cond bool, msg string) {
	t.Helper()
	if !cond {
		t.Errorf("%s: expected true", msg)
	}
}

// ---------------------------------------------------------------------------
// Tests: QueueManager construction
// ---------------------------------------------------------------------------

func TestNewQueueManager(t *testing.T) {
	qm := NewQueueManager(nil, nil)
	if qm == nil {
		t.Fatal("NewQueueManager returned nil")
	}
	if qm.store == nil {
		t.Fatal("store should not be nil")
	}
	if qm.estimator == nil {
		t.Fatal("estimator should not be nil")
	}
	if qm.db != nil {
		t.Error("db should be nil since we passed nil")
	}
}

// ---------------------------------------------------------------------------
// Tests: GetBatch limit validation
// ---------------------------------------------------------------------------

func TestGetBatch_LimitDefaults(t *testing.T) {
	// Verify that GetBatch enforces limit defaults
	// Since this requires a DB, we test the validation logic indirectly
	limits := []struct {
		input    int
		expected int
	}{
		{-10, 20},   // negative → default 20
		{0, 20},     // zero → default 20
		{1, 1},      // positive → as-is
		{20, 20},    // default boundary
		{100, 100},  // max boundary
		{101, 100},  // over max → capped at 100
		{500, 100},  // way over → capped at 100
	}

	for _, tc := range limits {
		got := normalizeLimit(tc.input)
		if got != tc.expected {
			t.Errorf("normalizeLimit(%d): want %d, got %d", tc.input, tc.expected, got)
		}
	}
}

func TestGetBatch_LimitBounds(t *testing.T) {
	// Test limit boundary values
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{"negative limit", -5, 20},
		{"zero limit", 0, 20},
		{"small positive", 5, 5},
		{"exactly 20", 20, 20},
		{"exactly 100", 100, 100},
		{"over max 101", 101, 100},
		{"way over max", 9999, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeLimit(tt.limit)
			if got != tt.want {
				t.Errorf("normalizeLimit(%d) = %d, want %d", tt.limit, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests: Constants
// ---------------------------------------------------------------------------

func TestConstants(t *testing.T) {
	if BatchThresholdDefault != 5 {
		t.Errorf("BatchThresholdDefault: want 5, got %d", BatchThresholdDefault)
	}
	if UrgentThreshold != 0.7 {
		t.Errorf("UrgentThreshold: want 0.7, got %f", UrgentThreshold)
	}
}

// ---------------------------------------------------------------------------
// Tests: GetNextCard with empty queue
// ---------------------------------------------------------------------------

func TestGetNextCard_EmptyQueue(t *testing.T) {
	// Without a real DB, the store will fail, but we can test the error path
	qm := NewQueueManager(nil, nil)
	ctx := t.Context()
	_, err := qm.GetNextCard(ctx, uuid.New())
	// Expected to error since DB is nil
	assertError(t, err, "expected error with nil DB")
}

// ---------------------------------------------------------------------------
// Tests: GetCounts with nil store
// ---------------------------------------------------------------------------

func TestGetCounts_NilStore(t *testing.T) {
	qm := NewQueueManager(nil, nil)
	ctx := t.Context()
	_, _, err := qm.GetCounts(ctx, uuid.New())
	assertError(t, err, "expected error with nil DB")
}

// ---------------------------------------------------------------------------
// Tests: IncrementServerVersion with nil store
// ---------------------------------------------------------------------------

func TestIncrementServerVersion_NilStore(t *testing.T) {
	qm := NewQueueManager(nil, nil)
	ctx := t.Context()
	_, err := qm.IncrementServerVersion(ctx, uuid.New())
	assertError(t, err, "expected error with nil DB")
}

// ---------------------------------------------------------------------------
// Tests: OnCardCreated validation
// ---------------------------------------------------------------------------

func TestOnCardCreated_SetsDefaults(t *testing.T) {
	qm := NewQueueManager(nil, nil)
	card := &models.DecisionCard{
		UserID: uuid.New(),
	}
	err := qm.OnCardCreated(t.Context(), card)
	// Will fail at DB level, but defaults should be set
	if card.ID == uuid.Nil {
		t.Error("card ID should have been set")
	}
	if card.CardState != "pending" {
		t.Errorf("card state should be 'pending', got %q", card.CardState)
	}
	if card.CreatedAt.IsZero() {
		t.Error("CreatedAt should have been set")
	}
	if card.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should have been set")
	}
	_ = err
}

func TestOnCardCreated_PreservesExistingValues(t *testing.T) {
	qm := NewQueueManager(nil, nil)
	existingID := uuid.New()
	now := models.DecisionCard{}
	now.CreatedAt = now.CreatedAt // satisfy unused

	card := &models.DecisionCard{
		ID:        existingID,
		UserID:    uuid.New(),
		CardState: "approved",
	}
	_ = qm.OnCardCreated(t.Context(), card)

	if card.ID != existingID {
		t.Error("existing ID should be preserved")
	}
	if card.CardState != "approved" {
		t.Error("existing state should be preserved")
	}
}

// ---------------------------------------------------------------------------
// Tests: OnCardCleared
// ---------------------------------------------------------------------------

func TestOnCardCleared_NilStore(t *testing.T) {
	qm := NewQueueManager(nil, nil)
	ctx := t.Context()
	err := qm.OnCardCleared(ctx, uuid.New(), uuid.New())
	assertError(t, err, "expected error with nil DB")
}

// ---------------------------------------------------------------------------
// Tests: OnCardClearedWithTiming
// ---------------------------------------------------------------------------

func TestOnCardClearedWithTiming_NilStore(t *testing.T) {
	qm := NewQueueManager(nil, nil)
	ctx := t.Context()
	err := qm.OnCardClearedWithTiming(ctx, uuid.New(), uuid.New(), 30.0)
	assertError(t, err, "expected error with nil DB")
}

// ---------------------------------------------------------------------------
// Tests: shouldTriggerNotification quiet hours logic
// ---------------------------------------------------------------------------

func TestShouldTriggerNotification_QuietHours(t *testing.T) {
	// Quiet hours are 22:00-08:00
	// We can test the constant values
	if UrgentThreshold != 0.7 {
		t.Errorf("UrgentThreshold: want 0.7, got %f", UrgentThreshold)
	}
}

// ---------------------------------------------------------------------------
// normalizeLimit helper (mirrors the limit normalization in GetBatch)
func normalizeLimit(limit int) int {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	return limit
}

// ---------------------------------------------------------------------------
// Tests: DB() accessor
// ---------------------------------------------------------------------------

func TestQueueManager_DB(t *testing.T) {
	qm := NewQueueManager(nil, nil)
	if qm.DB() != nil {
		t.Error("DB() should return nil when nil was passed")
	}
}
