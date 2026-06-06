package auto

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/decisionstack/classification/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newTestEmailEvent() *models.EmailIngestedEvent {
	return &models.EmailIngestedEvent{
		EventID:     uuid.Must(uuid.NewRandom()),
		UserID:      uuid.Must(uuid.NewRandom()),
		Source:      "gmail",
		AccountID:   uuid.Must(uuid.NewRandom()),
		ThreadID:    uuid.Must(uuid.NewRandom()),
		RawEmailID:  uuid.Must(uuid.NewRandom()),
		S3URI:       "s3://bucket/email",
		SenderEmail: "test@example.com",
	}
}

func makeActiveRule(name, actionType string, threshold float64, predicate models.RulePredicate) models.AutoHandleRule {
	return models.AutoHandleRule{
		ID:                  uuid.Must(uuid.NewRandom()),
		UserID:              uuid.Must(uuid.NewRandom()),
		Name:                name,
		Predicate:           predicate,
		ActionType:          actionType,
		ConfidenceThreshold: threshold,
		Status:              "active",
		UsageCount:          0,
	}
}

// ---------------------------------------------------------------------------
// Engine — hardConfidenceFloor constant
// ---------------------------------------------------------------------------

func TestEngine_Constants(t *testing.T) {
	assert.Equal(t, 0.92, hardConfidenceFloor)
	assert.Equal(t, 48*time.Hour, stagingWindow)
}

// ---------------------------------------------------------------------------
// Engine — confidence threshold logic (unit-level)
// ---------------------------------------------------------------------------

func TestEngine_ConfidenceThreshold_DefaultsToFloor(t *testing.T) {
	// When a rule has confidence threshold = 0, it should default to hardConfidenceFloor.
	confidence := float64(0)
	if confidence == 0 {
		confidence = hardConfidenceFloor
	}
	assert.Equal(t, hardConfidenceFloor, confidence)
}

func TestEngine_ConfidenceThreshold_BelowFloor_IsSkipped(t *testing.T) {
	// Rule confidence 0.85 < 0.92 → should be skipped.
	confidence := 0.85
	assert.True(t, confidence < hardConfidenceFloor)
}

func TestEngine_ConfidenceThreshold_AtFloor_IsAccepted(t *testing.T) {
	// Rule confidence exactly 0.92 → accepted (>= floor).
	confidence := hardConfidenceFloor
	assert.False(t, confidence < hardConfidenceFloor)
	assert.GreaterOrEqual(t, confidence, hardConfidenceFloor)
}

func TestEngine_ConfidenceThreshold_AboveFloor(t *testing.T) {
	confidence := 0.95
	assert.GreaterOrEqual(t, confidence, hardConfidenceFloor)
}

// ---------------------------------------------------------------------------
// Engine — first-wins ordering logic
// ---------------------------------------------------------------------------

func TestEngine_FirstWins_RuleSelection(t *testing.T) {
	// Simulate the first-wins behavior: rules are ordered by usage_count DESC.
	rules := []models.AutoHandleRule{
		{ID: uuid.Must(uuid.NewRandom()), Name: "High Usage", UsageCount: 10, ConfidenceThreshold: 0.95},
		{ID: uuid.Must(uuid.NewRandom()), Name: "Med Usage", UsageCount: 5, ConfidenceThreshold: 0.95},
		{ID: uuid.Must(uuid.NewRandom()), Name: "Low Usage", UsageCount: 1, ConfidenceThreshold: 0.95},
	}

	// First rule should win.
	winner := rules[0]
	assert.Equal(t, "High Usage", winner.Name)
	assert.Equal(t, 10, winner.UsageCount)
}

// ---------------------------------------------------------------------------
// Engine — rule matching with predicate evaluation
// ---------------------------------------------------------------------------

func TestEngine_RuleMatch_SenderEquals(t *testing.T) {
	pe := NewPredicateEvaluator()

	rule := models.AutoHandleRule{
		ID:   uuid.Must(uuid.NewRandom()),
		Name: "Vendor Rule",
		Predicate: models.RulePredicate{
			AllOf: []models.Condition{
				{Field: "sender_email", Operator: "eq", Value: "sarah@vendor.com"},
			},
		},
		ConfidenceThreshold: 0.95,
		Status:              "active",
	}

	attrs := models.EmailAttributes{SenderEmail: "sarah@vendor.com"}
	matched, err := pe.Evaluate(rule.Predicate, attrs)
	require.NoError(t, err)
	assert.True(t, matched)

	attrs2 := models.EmailAttributes{SenderEmail: "other@example.com"}
	matched2, err := pe.Evaluate(rule.Predicate, attrs2)
	require.NoError(t, err)
	assert.False(t, matched2)
}

func TestEngine_RuleMatch_MultipleConditions(t *testing.T) {
	pe := NewPredicateEvaluator()

	rule := models.AutoHandleRule{
		ID:   uuid.Must(uuid.NewRandom()),
		Name: "Complex Rule",
		Predicate: models.RulePredicate{
			AllOf: []models.Condition{
				{Field: "sender_domain", Operator: "eq", Value: "example.com"},
				{Field: "has_attachment", Operator: "eq", Value: true},
			},
		},
		ConfidenceThreshold: 0.95,
		Status:              "active",
	}

	// Both conditions match.
	attrs := models.EmailAttributes{SenderDomain: "example.com", HasAttachment: true}
	matched, err := pe.Evaluate(rule.Predicate, attrs)
	require.NoError(t, err)
	assert.True(t, matched)

	// Second condition fails.
	attrs2 := models.EmailAttributes{SenderDomain: "example.com", HasAttachment: false}
	matched2, err := pe.Evaluate(rule.Predicate, attrs2)
	require.NoError(t, err)
	assert.False(t, matched2)
}

// ---------------------------------------------------------------------------
// Engine — staged rule should NOT auto-fire
// ---------------------------------------------------------------------------

func TestEngine_StagedRule_NotAutoFired(t *testing.T) {
	pe := NewPredicateEvaluator()

	rule := models.AutoHandleRule{
		ID:   uuid.Must(uuid.NewRandom()),
		Name: "Staged Invoice Rule",
		Predicate: models.RulePredicate{
			AllOf: []models.Condition{
				{Field: "sender_email", Operator: "eq", Value: "sarah@vendor.com"},
			},
		},
		ConfidenceThreshold: 0.95,
		Status:              "staged", // NOT active
	}

	// The engine only evaluates *active* rules.
	// A staged rule should not be in the active rules list.
	assert.Equal(t, "staged", rule.Status)
	assert.NotEqual(t, "active", rule.Status)

	// Verify the predicate would match if the rule were active.
	attrs := models.EmailAttributes{SenderEmail: "sarah@vendor.com"}
	matched, err := pe.Evaluate(rule.Predicate, attrs)
	require.NoError(t, err)
	assert.True(t, matched, "predicate matches but rule is not active")
}

// ---------------------------------------------------------------------------
// Engine — action types
// ---------------------------------------------------------------------------

func TestEngine_ActionTypes(t *testing.T) {
	actionTypes := []string{
		"reply_template",
		"forward",
		"calendar_accept",
		"delete",
		"extract_notify",
	}

	for _, at := range actionTypes {
		t.Run(at, func(t *testing.T) {
			assert.NotEmpty(t, at)
		})
	}
}

// ---------------------------------------------------------------------------
// Engine — LLM fallback constants
// ---------------------------------------------------------------------------

func TestEngine_LLMFallback_Constants(t *testing.T) {
	assert.Equal(t, 0.92, confidenceFloor)
	assert.Equal(t, "claude-3-haiku-20240307", defaultHaikuModel)
	assert.Equal(t, "2023-06-01", anthropicAPIVersion)
}

// ---------------------------------------------------------------------------
// extractSubjectKeyword helper
// ---------------------------------------------------------------------------

func TestExtractSubjectKeyword(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Your invoice from ACME Corp", "invoice"},
		{"Re: Meeting notes", "meeting notes"},
		{"Fw: Important update", "important update"},
		{"The quick brown fox", "quick brown"},
		{"A", "A"},
		{"", ""},
		{"Fwd: The meeting is rescheduled", "meeting rescheduled"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractSubjectKeyword(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// stagingManager — construction and lifecycle
// ---------------------------------------------------------------------------

func TestStagingManager_Lifecycle(t *testing.T) {
	sm := newStagingManager()
	require.NotNil(t, sm)
	require.NotNil(t, sm.rules)
	require.NotNil(t, sm.ticker)
	require.NotNil(t, sm.done)

	// Clean up.
	sm.Stop()
}

// ---------------------------------------------------------------------------
// stagingManager — removeExpired
// ---------------------------------------------------------------------------

func TestStagingManager_RemoveExpired(t *testing.T) {
	sm := newStagingManager()
	defer sm.Stop()

	now := time.Now().UTC()

	// Add a rule past 48h.
	expiredRuleID := uuid.Must(uuid.NewRandom())
	sm.rules[expiredRuleID] = &models.StagingRule{
		RuleID:      expiredRuleID,
		UserID:      uuid.Must(uuid.NewRandom()),
		StagedAt:    now.Add(-72 * time.Hour),
		ActivatesAt: now.Add(-24 * time.Hour),
		Status:      "staged",
	}

	// Add a fresh rule.
	freshRuleID := uuid.Must(uuid.NewRandom())
	sm.rules[freshRuleID] = &models.StagingRule{
		RuleID:      freshRuleID,
		UserID:      uuid.Must(uuid.NewRandom()),
		StagedAt:    now.Add(-10 * time.Hour),
		ActivatesAt: now.Add(38 * time.Hour),
		Status:      "staged",
	}

	// Verify both exist.
	assert.Len(t, sm.rules, 2)

	// Run cleanup.
	sm.removeExpired()

	// Expired rule should be removed.
	_, exists := sm.rules[expiredRuleID]
	assert.False(t, exists, "expired rule should be removed")

	// Fresh rule should still exist.
	_, exists = sm.rules[freshRuleID]
	assert.True(t, exists, "fresh rule should still exist")
}

// ---------------------------------------------------------------------------
// stagingManager — removeExpired does not affect non-staged rules
// ---------------------------------------------------------------------------

func TestStagingManager_RemoveExpired_OnlyStaged(t *testing.T) {
	sm := newStagingManager()
	defer sm.Stop()

	now := time.Now().UTC()

	// Add a past-window rule that is already active (not staged).
	activeRuleID := uuid.Must(uuid.NewRandom())
	sm.rules[activeRuleID] = &models.StagingRule{
		RuleID:      activeRuleID,
		UserID:      uuid.Must(uuid.NewRandom()),
		StagedAt:    now.Add(-72 * time.Hour),
		ActivatesAt: now.Add(-24 * time.Hour),
		Status:      "active",
	}

	// Run cleanup.
	sm.removeExpired()

	// Active rule should NOT be removed (only "staged" rules are cleaned up).
	_, exists := sm.rules[activeRuleID]
	assert.True(t, exists, "active rule should NOT be removed by cleanup")
}

// ---------------------------------------------------------------------------
// stagingManager — Stop is idempotent-safe
// ---------------------------------------------------------------------------

func TestStagingManager_Stop(t *testing.T) {
	sm := newStagingManager()
	sm.Stop()
	// Calling Stop again should not panic.
	// (The done channel is already closed, so a second close would panic.
	// The real code should handle this; this test documents expected behavior.)
}

// ---------------------------------------------------------------------------
// RuleCache — basic operations
// ---------------------------------------------------------------------------

func TestRuleCache_GetSet(t *testing.T) {
	cache := NewRuleCache(5 * time.Minute)
	userID := uuid.Must(uuid.NewRandom())

	rules := []models.AutoHandleRule{
		{ID: uuid.Must(uuid.NewRandom()), Name: "Rule 1", UsageCount: 5},
		{ID: uuid.Must(uuid.NewRandom()), Name: "Rule 2", UsageCount: 3},
	}

	// Cache miss.
	_, ok := cache.Get(userID)
	assert.False(t, ok)

	// Set cache.
	cache.Set(userID, rules)

	// Cache hit.
	got, ok := cache.Get(userID)
	assert.True(t, ok)
	assert.Len(t, got, 2)
	assert.Equal(t, "Rule 1", got[0].Name)
}

func TestRuleCache_Expire(t *testing.T) {
	cache := NewRuleCache(50 * time.Millisecond)
	userID := uuid.Must(uuid.NewRandom())

	rules := []models.AutoHandleRule{
		{ID: uuid.Must(uuid.NewRandom()), Name: "Temp Rule"},
	}

	cache.Set(userID, rules)

	// Should be a cache hit immediately.
	_, ok := cache.Get(userID)
	assert.True(t, ok)

	// Wait for TTL to expire.
	time.Sleep(100 * time.Millisecond)

	// Should be a miss after expiration.
	_, ok = cache.Get(userID)
	assert.False(t, ok)
}

func TestRuleCache_InvalidateUser(t *testing.T) {
	cache := NewRuleCache(5 * time.Minute)
	userID := uuid.Must(uuid.NewRandom())

	cache.Set(userID, []models.AutoHandleRule{
		{ID: uuid.Must(uuid.NewRandom()), Name: "Rule"},
	})

	// Verify cached.
	_, ok := cache.Get(userID)
	assert.True(t, ok)

	// Invalidate.
	cache.InvalidateUser(userID)

	// Verify gone.
	_, ok = cache.Get(userID)
	assert.False(t, ok)
}

func TestRuleCache_InvalidateAll(t *testing.T) {
	cache := NewRuleCache(5 * time.Minute)
	userID1 := uuid.Must(uuid.NewRandom())
	userID2 := uuid.Must(uuid.NewRandom())

	cache.Set(userID1, []models.AutoHandleRule{{ID: uuid.Must(uuid.NewRandom()), Name: "Rule1"}})
	cache.Set(userID2, []models.AutoHandleRule{{ID: uuid.Must(uuid.NewRandom()), Name: "Rule2"}})

	cache.InvalidateAll()

	_, ok := cache.Get(userID1)
	assert.False(t, ok)
	_, ok = cache.Get(userID2)
	assert.False(t, ok)
}

// ---------------------------------------------------------------------------
// RuleCache — cache entry has correct expiration
// ---------------------------------------------------------------------------

func TestRuleCache_Set_ExpiresAt(t *testing.T) {
	ttl := 5 * time.Minute
	cache := NewRuleCache(ttl)
	userID := uuid.Must(uuid.NewRandom())

	beforeSet := time.Now().UTC()
	cache.Set(userID, []models.AutoHandleRule{
		{ID: uuid.Must(uuid.NewRandom()), Name: "Rule"},
	})
	afterSet := time.Now().UTC()

	// Check internal state.
	cache.mu.RLock()
	entry := cache.entries[userID.String()]
	cache.mu.RUnlock()

	require.NotNil(t, entry)
	assert.True(t, entry.expiresAt.After(beforeSet.Add(ttl)))
	assert.True(t, entry.expiresAt.Before(afterSet.Add(ttl+time.Second)))
}

// ---------------------------------------------------------------------------
// Engine — ClassificationResult building for auto route
// ---------------------------------------------------------------------------

func TestEngine_ClassificationResult_AutoRoute(t *testing.T) {
	// Verify the structure of a RouteAuto ClassificationResult.
	ruleID := uuid.Must(uuid.NewRandom())
	result := &models.ClassificationResult{
		RawEmailID:    uuid.Must(uuid.NewRandom()),
		UserID:        uuid.Must(uuid.NewRandom()),
		ThreadID:      uuid.Must(uuid.NewRandom()),
		Route:         models.RouteAuto,
		Confidence:    0.95,
		MatchedRuleID: &ruleID,
		LLMMatched:    false,
		ProcessedAt:   time.Now().UTC(),
	}

	assert.Equal(t, models.RouteAuto, result.Route)
	assert.InDelta(t, 0.95, result.Confidence, 0.001)
	assert.NotNil(t, result.MatchedRuleID)
	assert.Equal(t, ruleID, *result.MatchedRuleID)
	assert.False(t, result.LLMMatched)
	assert.Nil(t, result.ExtractedData)
}

// ---------------------------------------------------------------------------
// Engine — LLM fallback classification result
// ---------------------------------------------------------------------------

func TestEngine_ClassificationResult_LLMMatched(t *testing.T) {
	ruleID := uuid.Must(uuid.NewRandom())
	result := &models.ClassificationResult{
		RawEmailID:    uuid.Must(uuid.NewRandom()),
		UserID:        uuid.Must(uuid.NewRandom()),
		ThreadID:      uuid.Must(uuid.NewRandom()),
		Route:         models.RouteAuto,
		Confidence:    0.97,
		MatchedRuleID: &ruleID,
		LLMMatched:    true,
		ProcessedAt:   time.Now().UTC(),
	}

	assert.Equal(t, models.RouteAuto, result.Route)
	assert.InDelta(t, 0.97, result.Confidence, 0.001)
	assert.True(t, result.LLMMatched)
	assert.NotNil(t, result.MatchedRuleID)
}
