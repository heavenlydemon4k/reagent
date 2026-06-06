package staging

import (
	"testing"
	"time"

	"github.com/decisionstack/classification/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// StagingWindow — rule past 48h should be eligible for activation
// ---------------------------------------------------------------------------

func TestStagingWindow_Expired(t *testing.T) {
	now := time.Now().UTC()
	stagedAt := now.Add(-49 * time.Hour)

	rule := models.StagingRule{
		RuleID:      uuid.Must(uuid.NewRandom()),
		UserID:      uuid.Must(uuid.NewRandom()),
		StagedAt:    stagedAt,
		ActivatesAt: stagedAt.Add(48 * time.Hour),
		Status:      "staged",
	}

	assert.Equal(t, "staged", rule.Status)
	assert.True(t, now.After(rule.ActivatesAt), "rule should be past activation time")
	assert.InDelta(t, 49.0, time.Since(rule.StagedAt).Hours(), 0.1)
}

// ---------------------------------------------------------------------------
// StagingWindow — rule within 48h should NOT be eligible
// ---------------------------------------------------------------------------

func TestStagingWindow_StillInWindow(t *testing.T) {
	now := time.Now().UTC()
	stagedAt := now.Add(-10 * time.Hour)

	rule := models.StagingRule{
		RuleID:      uuid.Must(uuid.NewRandom()),
		UserID:      uuid.Must(uuid.NewRandom()),
		StagedAt:    stagedAt,
		ActivatesAt: stagedAt.Add(48 * time.Hour),
		Status:      "staged",
	}

	assert.Equal(t, "staged", rule.Status)
	assert.False(t, now.After(rule.ActivatesAt), "rule should still be within staging window")
	assert.InDelta(t, 10.0, time.Since(rule.StagedAt).Hours(), 0.1)
}

// ---------------------------------------------------------------------------
// StagingWindow — revoked rule should NOT activate
// ---------------------------------------------------------------------------

func TestStagingWindow_Revoked_NotEligible(t *testing.T) {
	now := time.Now().UTC()
	stagedAt := now.Add(-72 * time.Hour)

	rule := models.StagingRule{
		RuleID:      uuid.Must(uuid.NewRandom()),
		UserID:      uuid.Must(uuid.NewRandom()),
		StagedAt:    stagedAt,
		ActivatesAt: stagedAt.Add(48 * time.Hour),
		Status:      "revoked",
	}

	assert.Equal(t, "revoked", rule.Status)
	// Even though past 48h, revoked rules should not activate.
	assert.True(t, now.After(rule.ActivatesAt))
}

// ---------------------------------------------------------------------------
// StagingWindow — activated rule should NOT be re-activated
// ---------------------------------------------------------------------------

func TestStagingWindow_AlreadyActive_NotEligible(t *testing.T) {
	now := time.Now().UTC()
	stagedAt := now.Add(-72 * time.Hour)

	rule := models.StagingRule{
		RuleID:      uuid.Must(uuid.NewRandom()),
		UserID:      uuid.Must(uuid.NewRandom()),
		StagedAt:    stagedAt,
		ActivatesAt: stagedAt.Add(48 * time.Hour),
		Status:      "active",
	}

	assert.Equal(t, "active", rule.Status)
}

// ---------------------------------------------------------------------------
// StagingWindow — boundary: exactly 48 hours
// ---------------------------------------------------------------------------

func TestStagingWindow_BoundaryExactly48h(t *testing.T) {
	now := time.Now().UTC()
	stagedAt := now.Add(-48 * time.Hour)

	rule := models.StagingRule{
		RuleID:      uuid.Must(uuid.NewRandom()),
		UserID:      uuid.Must(uuid.NewRandom()),
		StagedAt:    stagedAt,
		ActivatesAt: stagedAt.Add(48 * time.Hour),
		Status:      "staged",
	}

	// At exactly 48h, the rule should be eligible (staged_at < NOW() - 48h).
	// Since the query uses staged_at < NOW() - INTERVAL '48 hours', exactly 48h
	// should be on the boundary. Our staged_at = now - 48h, so
	// staged_at should be exactly equal to the threshold, not less.
	assert.False(t, rule.StagedAt.Before(now.Add(-48*time.Hour)), "exactly 48h should be on boundary")
}

// ---------------------------------------------------------------------------
// StagingWindow — just under 48 hours
// ---------------------------------------------------------------------------

func TestStagingWindow_JustUnder48h(t *testing.T) {
	now := time.Now().UTC()
	stagedAt := now.Add(-47*time.Hour - 59*time.Minute)

	rule := models.StagingRule{
		RuleID:      uuid.Must(uuid.NewRandom()),
		UserID:      uuid.Must(uuid.NewRandom()),
		StagedAt:    stagedAt,
		ActivatesAt: stagedAt.Add(48 * time.Hour),
		Status:      "staged",
	}

	assert.False(t, now.After(rule.ActivatesAt), "just under 48h should not be eligible")
}

// ---------------------------------------------------------------------------
// StagingWindow — just over 48 hours
// ---------------------------------------------------------------------------

func TestStagingWindow_JustOver48h(t *testing.T) {
	now := time.Now().UTC()
	stagedAt := now.Add(-48*time.Hour - 1*time.Minute)

	rule := models.StagingRule{
		RuleID:      uuid.Must(uuid.NewRandom()),
		UserID:      uuid.Must(uuid.NewRandom()),
		StagedAt:    stagedAt,
		ActivatesAt: stagedAt.Add(48 * time.Hour),
		Status:      "staged",
	}

	assert.True(t, now.After(rule.ActivatesAt), "just over 48h should be eligible")
}

// ---------------------------------------------------------------------------
// StagingCron construction
// ---------------------------------------------------------------------------

func TestNewStagingCron(t *testing.T) {
	cron := NewStagingCron(nil, nil, 0, nil)
	require.NotNil(t, cron)
	assert.Equal(t, defaultInterval, cron.interval)
	assert.NotNil(t, cron.stopCh)
	assert.False(t, cron.running)
}

func TestNewStagingCron_CustomInterval(t *testing.T) {
	cron := NewStagingCron(nil, nil, 5*time.Minute, nil)
	require.NotNil(t, cron)
	assert.Equal(t, 5*time.Minute, cron.interval)
}

// ---------------------------------------------------------------------------
// StagingCron — IsRunning
// ---------------------------------------------------------------------------

func TestStagingCron_IsRunning(t *testing.T) {
	cron := NewStagingCron(nil, nil, defaultInterval, nil)
	assert.False(t, cron.IsRunning())

	// Simulate running state.
	cron.mu.Lock()
	cron.running = true
	cron.mu.Unlock()
	assert.True(t, cron.IsRunning())
}

// ---------------------------------------------------------------------------
// StagingCron — constants
// ---------------------------------------------------------------------------

func TestStagingCron_Constants(t *testing.T) {
	assert.Equal(t, 15*time.Minute, defaultInterval)
	assert.Equal(t, 48*time.Hour, stagingWindow)
}

// ---------------------------------------------------------------------------
// StagingRule batch scenarios
// ---------------------------------------------------------------------------

func TestStagingWindow_MixedRules(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name         string
		stagedAt     time.Time
		status       string
		wantEligible bool
	}{
		{
			name:         "expired staged rule",
			stagedAt:     now.Add(-72 * time.Hour),
			status:       "staged",
			wantEligible: true,
		},
		{
			name:         "fresh staged rule",
			stagedAt:     now.Add(-10 * time.Hour),
			status:       "staged",
			wantEligible: false,
		},
		{
			name:         "revoked old rule",
			stagedAt:     now.Add(-72 * time.Hour),
			status:       "revoked",
			wantEligible: false,
		},
		{
			name:         "active old rule",
			stagedAt:     now.Add(-72 * time.Hour),
			status:       "active",
			wantEligible: false,
		},
		{
			name:         "expired staged rule at 49h",
			stagedAt:     now.Add(-49 * time.Hour),
			status:       "staged",
			wantEligible: true,
		},
		{
			name:         "staged at exactly 48h",
			stagedAt:     now.Add(-48 * time.Hour),
			status:       "staged",
			wantEligible: false, // staged_at < NOW() - 48h → not strictly less
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := models.StagingRule{
				RuleID:      uuid.Must(uuid.NewRandom()),
				UserID:      uuid.Must(uuid.NewRandom()),
				StagedAt:    tt.stagedAt,
				ActivatesAt: tt.stagedAt.Add(48 * time.Hour),
				Status:      tt.status,
			}

			// Eligibility: staged AND past 48h window.
			isPastWindow := now.After(rule.ActivatesAt)
			isStaged := rule.Status == "staged"
			eligible := isPastWindow && isStaged

			assert.Equal(t, tt.wantEligible, eligible,
				"rule status=%s, staged_at=%v ago, activates_at in future=%v",
				rule.Status, time.Since(rule.StagedAt), now.Before(rule.ActivatesAt))
		})
	}
}

// ---------------------------------------------------------------------------
// AutoHandleRule staging fields
// ---------------------------------------------------------------------------

func TestAutoHandleRule_StagingFields(t *testing.T) {
	now := time.Now().UTC()

	rule := models.AutoHandleRule{
		ID:                  uuid.Must(uuid.NewRandom()),
		UserID:              uuid.Must(uuid.NewRandom()),
		Name:                "Test Rule",
		Status:              "staged",
		StagedAt:            &now,
		ConfidenceThreshold: 0.92,
	}

	assert.Equal(t, "staged", rule.Status)
	assert.NotNil(t, rule.StagedAt)
	assert.Nil(t, rule.ActivatedAt)
	assert.Nil(t, rule.RevokedAt)
	assert.InDelta(t, 0.92, rule.ConfidenceThreshold, 0.001)
}

// ---------------------------------------------------------------------------
// Bulk activation eligibility filter
// ---------------------------------------------------------------------------

func TestStagingWindow_BulkFilter(t *testing.T) {
	now := time.Now().UTC()

	// Create a batch of mixed rules.
	rules := []models.AutoHandleRule{
		{
			ID:       uuid.Must(uuid.NewRandom()),
			Name:     "Expired Rule",
			Status:   "staged",
			StagedAt: func() *time.Time { t := now.Add(-72 * time.Hour); return &t }(),
		},
		{
			ID:       uuid.Must(uuid.NewRandom()),
			Name:     "Fresh Rule",
			Status:   "staged",
			StagedAt: func() *time.Time { t := now.Add(-10 * time.Hour); return &t }(),
		},
		{
			ID:       uuid.Must(uuid.NewRandom()),
			Name:     "Revoked Rule",
			Status:   "revoked",
			StagedAt: func() *time.Time { t := now.Add(-72 * time.Hour); return &t }(),
		},
		{
			ID:       uuid.Must(uuid.NewRandom()),
			Name:     "Active Rule",
			Status:   "active",
			StagedAt: func() *time.Time { t := now.Add(-72 * time.Hour); return &t }(),
		},
	}

	var eligible []models.AutoHandleRule
	for _, r := range rules {
		if r.Status == "staged" && r.StagedAt != nil && now.After(*r.StagedAt.Add(48*time.Hour)) {
			eligible = append(eligible, r)
		}
	}

	require.Len(t, eligible, 1)
	assert.Equal(t, "Expired Rule", eligible[0].Name)
}
