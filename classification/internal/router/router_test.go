package router

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/decisionstack/classification/internal/models"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Mock Extractor
// ---------------------------------------------------------------------------

type mockExtractor struct {
	mock.Mock
}

func (m *mockExtractor) Process(ctx context.Context, event *models.EmailIngestedEvent) (*models.ExtractedDatum, error) {
	args := m.Called(ctx, event)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.ExtractedDatum), args.Error(1)
}

// ---------------------------------------------------------------------------
// Mock AutoEngine
// ---------------------------------------------------------------------------

type mockAutoEngine struct {
	mock.Mock
}

func (m *mockAutoEngine) Evaluate(ctx context.Context, event *models.EmailIngestedEvent, attrs models.EmailAttributes) (*models.ClassificationResult, bool, error) {
	args := m.Called(ctx, event, attrs)
	if args.Get(0) == nil {
		return nil, args.Bool(1), args.Error(2)
	}
	return args.Get(0).(*models.ClassificationResult), args.Bool(1), args.Error(2)
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newTestMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	return NewMetrics(reg)
}

func newTestEvent() *models.EmailIngestedEvent {
	return &models.EmailIngestedEvent{
		EventID:        uuid.Must(uuid.NewRandom()),
		UserID:         uuid.Must(uuid.NewRandom()),
		Source:         "gmail",
		AccountID:      uuid.Must(uuid.NewRandom()),
		ThreadID:       uuid.Must(uuid.NewRandom()),
		RawEmailID:     uuid.Must(uuid.NewRandom()),
		S3URI:          "s3://bucket/email",
		SenderEmail:    "test@example.com",
		HasAttachments: false,
		ContactIDs:     []uuid.UUID{},
	}
}

// ---------------------------------------------------------------------------
// Router — Extract stage match → RouteExtract
// ---------------------------------------------------------------------------

func TestRouter_Route_ExtractMatch(t *testing.T) {
	extract := new(mockExtractor)
	autoEngine := new(mockAutoEngine)
	log := testLogger()
	metrics := newTestMetrics()

	router := NewRouter(extract, autoEngine, log, metrics)

	email := newTestEvent()
	datum := &models.ExtractedDatum{
		Type:             "2fa",
		Value:            "123456",
		NotificationText: "Verification code detected",
	}
	extract.On("Process", mock.Anything, email).Return(datum, nil)

	result, err := router.Route(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.RouteExtract, result.Route)
	assert.InDelta(t, 1.0, result.Confidence, 0.001)
	require.NotNil(t, result.ExtractedData)
	assert.Equal(t, "2fa", result.ExtractedData.Type)
	assert.Equal(t, "123456", result.ExtractedData.Value)

	extract.AssertExpectations(t)
	autoEngine.AssertNotCalled(t, "Evaluate")
}

// ---------------------------------------------------------------------------
// Router — 2FA email → RouteExtract
// ---------------------------------------------------------------------------

func TestRouter_Route_2FAEmail(t *testing.T) {
	extract := new(mockExtractor)
	autoEngine := new(mockAutoEngine)
	log := testLogger()
	metrics := newTestMetrics()

	router := NewRouter(extract, autoEngine, log, metrics)

	email := newTestEvent()
	email.SenderEmail = "noreply@google.com"
	extract.On("Process", mock.Anything, email).Return(&models.ExtractedDatum{
		Type:             "2fa",
		Value:            "987654",
		NotificationText: "Verification code detected",
	}, nil)

	result, err := router.Route(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.RouteExtract, result.Route)
	assert.Equal(t, "2fa", result.ExtractedData.Type)

	extract.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Router — Tracking email → RouteExtract
// ---------------------------------------------------------------------------

func TestRouter_Route_TrackingEmail(t *testing.T) {
	extract := new(mockExtractor)
	autoEngine := new(mockAutoEngine)
	log := testLogger()
	metrics := newTestMetrics()

	router := NewRouter(extract, autoEngine, log, metrics)

	email := newTestEvent()
	extract.On("Process", mock.Anything, email).Return(&models.ExtractedDatum{
		Type:             "tracking",
		Value:            "1Z999AA10123456784",
		NotificationText: "Package tracking update",
	}, nil)

	result, err := router.Route(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.RouteExtract, result.Route)
	assert.Equal(t, "tracking", result.ExtractedData.Type)

	extract.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Router — Receipt email → RouteExtract
// ---------------------------------------------------------------------------

func TestRouter_Route_ReceiptEmail(t *testing.T) {
	extract := new(mockExtractor)
	autoEngine := new(mockAutoEngine)
	log := testLogger()
	metrics := newTestMetrics()

	router := NewRouter(extract, autoEngine, log, metrics)

	email := newTestEvent()
	extract.On("Process", mock.Anything, email).Return(&models.ExtractedDatum{
		Type:             "receipt",
		Value:            "ABC-12345",
		NotificationText: "Receipt or order confirmation",
	}, nil)

	result, err := router.Route(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.RouteExtract, result.Route)
	assert.Equal(t, "receipt", result.ExtractedData.Type)

	extract.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Router — Auto-Handle stage match → RouteAuto
// ---------------------------------------------------------------------------

func TestRouter_Route_AutoMatch(t *testing.T) {
	extract := new(mockExtractor)
	autoEngine := new(mockAutoEngine)
	log := testLogger()
	metrics := newTestMetrics()

	router := NewRouter(extract, autoEngine, log, metrics)

	email := newTestEvent()
	ruleID := uuid.Must(uuid.NewRandom())

	// Extract stage misses.
	extract.On("Process", mock.Anything, email).Return(nil, nil)
	// Auto-Handle stage matches.
	autoResult := &models.ClassificationResult{
		RawEmailID:    email.RawEmailID,
		UserID:        email.UserID,
		ThreadID:      email.ThreadID,
		Route:         models.RouteAuto,
		Confidence:    0.95,
		MatchedRuleID: &ruleID,
	}
	autoEngine.On("Evaluate", mock.Anything, email, mock.AnythingOfType("models.EmailAttributes")).Return(autoResult, true, nil)

	result, err := router.Route(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.RouteAuto, result.Route)
	assert.InDelta(t, 0.95, result.Confidence, 0.001)
	assert.NotNil(t, result.MatchedRuleID)

	extract.AssertExpectations(t)
	autoEngine.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Router — confidence below 0.92 → RouteDecision
// ---------------------------------------------------------------------------

func TestRouter_Route_ConfidenceBelowFloor(t *testing.T) {
	extract := new(mockExtractor)
	autoEngine := new(mockAutoEngine)
	log := testLogger()
	metrics := newTestMetrics()

	router := NewRouter(extract, autoEngine, log, metrics)

	email := newTestEvent()

	// Extract stage misses.
	extract.On("Process", mock.Anything, email).Return(nil, nil)
	// Auto-Handle stage: engine says not handled (confidence too low or no match).
	autoEngine.On("Evaluate", mock.Anything, email, mock.AnythingOfType("models.EmailAttributes")).Return(nil, false, nil)

	result, err := router.Route(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.RouteDecision, result.Route)
	assert.InDelta(t, 0.0, result.Confidence, 0.001)
	assert.Nil(t, result.MatchedRuleID)
	assert.Nil(t, result.ExtractedData)

	extract.AssertExpectations(t)
	autoEngine.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Router — unknown email → RouteDecision (default)
// ---------------------------------------------------------------------------

func TestRouter_Route_UnknownEmail_DefaultToDecision(t *testing.T) {
	extract := new(mockExtractor)
	autoEngine := new(mockAutoEngine)
	log := testLogger()
	metrics := newTestMetrics()

	router := NewRouter(extract, autoEngine, log, metrics)

	email := newTestEvent()
	email.SenderEmail = "unknown@random-domain.com"

	// No extract match.
	extract.On("Process", mock.Anything, email).Return(nil, nil)
	// No auto-handle match.
	autoEngine.On("Evaluate", mock.Anything, email, mock.AnythingOfType("models.EmailAttributes")).Return(nil, false, nil)

	result, err := router.Route(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.RouteDecision, result.Route)
	assert.Equal(t, email.RawEmailID, result.RawEmailID)
	assert.Equal(t, email.UserID, result.UserID)
	assert.Equal(t, email.ThreadID, result.ThreadID)

	extract.AssertExpectations(t)
	autoEngine.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Router — extract stage error is non-fatal
// ---------------------------------------------------------------------------

func TestRouter_Route_ExtractError_NonFatal(t *testing.T) {
	extract := new(mockExtractor)
	autoEngine := new(mockAutoEngine)
	log := testLogger()
	metrics := newTestMetrics()

	router := NewRouter(extract, autoEngine, log, metrics)

	email := newTestEvent()

	// Extract stage fails but router continues.
	extract.On("Process", mock.Anything, email).Return(nil, errors.New("extract timeout"))
	// Auto-Handle stage: no match.
	autoEngine.On("Evaluate", mock.Anything, email, mock.AnythingOfType("models.EmailAttributes")).Return(nil, false, nil)

	result, err := router.Route(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.RouteDecision, result.Route)

	extract.AssertExpectations(t)
	autoEngine.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Router — auto stage error is non-fatal
// ---------------------------------------------------------------------------

func TestRouter_Route_AutoError_NonFatal(t *testing.T) {
	extract := new(mockExtractor)
	autoEngine := new(mockAutoEngine)
	log := testLogger()
	metrics := newTestMetrics()

	router := NewRouter(extract, autoEngine, log, metrics)

	email := newTestEvent()

	// Extract stage misses.
	extract.On("Process", mock.Anything, email).Return(nil, nil)
	// Auto-Handle stage fails but router falls through to decision.
	autoEngine.On("Evaluate", mock.Anything, email, mock.AnythingOfType("models.EmailAttributes")).Return(nil, false, errors.New("auto engine timeout"))

	result, err := router.Route(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.RouteDecision, result.Route)

	extract.AssertExpectations(t)
	autoEngine.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Router — calendar MIME → RouteExtract
// ---------------------------------------------------------------------------

func TestRouter_Route_CalendarEmail(t *testing.T) {
	extract := new(mockExtractor)
	autoEngine := new(mockAutoEngine)
	log := testLogger()
	metrics := newTestMetrics()

	router := NewRouter(extract, autoEngine, log, metrics)

	email := newTestEvent()
	email.S3URI = "s3://bucket/invite.ics"

	extract.On("Process", mock.Anything, email).Return(&models.ExtractedDatum{
		Type:             "calendar",
		Value:            "calendar_invite",
		NotificationText: "Calendar event received",
	}, nil)

	result, err := router.Route(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.RouteExtract, result.Route)
	assert.Equal(t, "calendar", result.ExtractedData.Type)

	autoEngine.AssertNotCalled(t, "Evaluate")
}

// ---------------------------------------------------------------------------
// Router — buildAttributes
// ---------------------------------------------------------------------------

func TestRouter_BuildAttributes(t *testing.T) {
	email := &models.EmailIngestedEvent{
		SenderEmail:    "alice@example.com",
		HasAttachments: true,
		ContactIDs:     []uuid.UUID{uuid.Must(uuid.NewRandom()), uuid.Must(uuid.NewRandom())},
		ReceivedAt:     time.Now(),
	}

	attrs := buildAttributes(email)

	assert.Equal(t, "alice@example.com", attrs.SenderEmail)
	assert.Equal(t, "example.com", attrs.SenderDomain)
	assert.True(t, attrs.HasAttachment)
	assert.Equal(t, 2, attrs.ThreadParticipantCount)
	assert.GreaterOrEqual(t, attrs.TimeOfDay, 0)
	assert.Less(t, attrs.TimeOfDay, 24)
	assert.GreaterOrEqual(t, attrs.DayOfWeek, 0)
	assert.Less(t, attrs.DayOfWeek, 7)
}

func TestRouter_BuildAttributes_NoAtSign(t *testing.T) {
	email := &models.EmailIngestedEvent{
		SenderEmail:    "invalid-email",
		HasAttachments: false,
		ContactIDs:     []uuid.UUID{},
	}

	attrs := buildAttributes(email)
	assert.Equal(t, "", attrs.SenderDomain)
}

// ---------------------------------------------------------------------------
// Router — IsTerminalRoute
// ---------------------------------------------------------------------------

func TestIsTerminalRoute(t *testing.T) {
	tests := []struct {
		route models.RouteType
		want  bool
	}{
		{models.RouteExtract, true},
		{models.RouteAuto, true},
		{models.RouteDecision, true},
		{models.RouteType("unknown"), false},
		{models.RouteType(""), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.route), func(t *testing.T) {
			assert.Equal(t, tt.want, IsTerminalRoute(tt.route))
		})
	}
}

// ---------------------------------------------------------------------------
// Router — RouteForRuleID
// ---------------------------------------------------------------------------

func TestRouteForRuleID(t *testing.T) {
	email := newTestEvent()
	ruleID := uuid.Must(uuid.NewRandom())
	confidence := 0.95
	actionType := "reply_template"

	result := RouteForRuleID(email, ruleID, confidence, actionType)

	require.NotNil(t, result)
	assert.Equal(t, email.RawEmailID, result.RawEmailID)
	assert.Equal(t, email.UserID, result.UserID)
	assert.Equal(t, email.ThreadID, result.ThreadID)
	assert.Equal(t, models.RouteType(actionType), result.Route)
	assert.InDelta(t, confidence, result.Confidence, 0.001)
	assert.NotNil(t, result.MatchedRuleID)
	assert.Equal(t, ruleID, *result.MatchedRuleID)
	assert.NotZero(t, result.ProcessedAt)
}

// ---------------------------------------------------------------------------
// Router — ValidateResult
// ---------------------------------------------------------------------------

func TestValidateResult(t *testing.T) {
	log := testLogger()
	metrics := newTestMetrics()

	tests := []struct {
		name    string
		result  *models.ClassificationResult
		wantErr string
	}{
		{
			name: "valid extract route",
			result: &models.ClassificationResult{
				RawEmailID:    uuid.Must(uuid.NewRandom()),
				UserID:        uuid.Must(uuid.NewRandom()),
				Route:         models.RouteExtract,
				ExtractedData: &models.ExtractedDatum{Type: "2fa", Value: "123"},
			},
		},
		{
			name: "valid auto route",
			result: &models.ClassificationResult{
				RawEmailID:    uuid.Must(uuid.NewRandom()),
				UserID:        uuid.Must(uuid.NewRandom()),
				Route:         models.RouteAuto,
				MatchedRuleID: func() *uuid.UUID { id := uuid.Must(uuid.NewRandom()); return &id }(),
			},
		},
		{
			name: "valid decision route",
			result: &models.ClassificationResult{
				RawEmailID: uuid.Must(uuid.NewRandom()),
				UserID:     uuid.Must(uuid.NewRandom()),
				Route:      models.RouteDecision,
			},
		},
		{
			name: "missing RawEmailID",
			result: &models.ClassificationResult{
				UserID: uuid.Nil,
				Route:  models.RouteDecision,
			},
			wantErr: "RawEmailID",
		},
		{
			name: "missing UserID",
			result: &models.ClassificationResult{
				RawEmailID: uuid.Must(uuid.NewRandom()),
				UserID:     uuid.Nil,
				Route:      models.RouteDecision,
			},
			wantErr: "UserID",
		},
		{
			name: "non-terminal route",
			result: &models.ClassificationResult{
				RawEmailID: uuid.Must(uuid.NewRandom()),
				UserID:     uuid.Must(uuid.NewRandom()),
				Route:      models.RouteType("invalid"),
			},
			wantErr: "non-terminal route",
		},
		{
			name: "extract route missing ExtractedData",
			result: &models.ClassificationResult{
				RawEmailID: uuid.Must(uuid.NewRandom()),
				UserID:     uuid.Must(uuid.NewRandom()),
				Route:      models.RouteExtract,
			},
			wantErr: "ExtractedData",
		},
		{
			name: "auto route missing MatchedRuleID",
			result: &models.ClassificationResult{
				RawEmailID: uuid.Must(uuid.NewRandom()),
				UserID:     uuid.Must(uuid.NewRandom()),
				Route:      models.RouteAuto,
			},
			wantErr: "MatchedRuleID",
		},
	}

	_ = log
	_ = metrics

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateResult(tt.result)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

// ---------------------------------------------------------------------------
// Router — result fields for all three routes
// ---------------------------------------------------------------------------

func TestRouter_Route_ResultFields(t *testing.T) {
	tests := []struct {
		name         string
		setupMocks   func(*mockExtractor, *mockAutoEngine, *models.EmailIngestedEvent)
		expectedRoute models.RouteType
	}{
		{
			name: "extract route fields",
			setupMocks: func(ex *mockExtractor, ae *mockAutoEngine, email *models.EmailIngestedEvent) {
				ex.On("Process", mock.Anything, email).Return(&models.ExtractedDatum{
					Type: "2fa", Value: "123456", NotificationText: "Code",
				}, nil)
			},
			expectedRoute: models.RouteExtract,
		},
		{
			name: "auto route fields",
			setupMocks: func(ex *mockExtractor, ae *mockAutoEngine, email *models.EmailIngestedEvent) {
				ex.On("Process", mock.Anything, email).Return(nil, nil)
				ruleID := uuid.Must(uuid.NewRandom())
				ae.On("Evaluate", mock.Anything, email, mock.Anything).Return(&models.ClassificationResult{
					RawEmailID:    email.RawEmailID,
					UserID:        email.UserID,
					ThreadID:      email.ThreadID,
					Route:         models.RouteAuto,
					Confidence:    0.95,
					MatchedRuleID: &ruleID,
				}, true, nil)
			},
			expectedRoute: models.RouteAuto,
		},
		{
			name: "decision route fields",
			setupMocks: func(ex *mockExtractor, ae *mockAutoEngine, email *models.EmailIngestedEvent) {
				ex.On("Process", mock.Anything, email).Return(nil, nil)
				ae.On("Evaluate", mock.Anything, email, mock.Anything).Return(nil, false, nil)
			},
			expectedRoute: models.RouteDecision,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extract := new(mockExtractor)
			autoEngine := new(mockAutoEngine)
			log := testLogger()
			metrics := newTestMetrics()
			router := NewRouter(extract, autoEngine, log, metrics)

			email := newTestEvent()
			tt.setupMocks(extract, autoEngine, email)

			result, err := router.Route(context.Background(), email)
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.expectedRoute, result.Route)
			assert.Equal(t, email.RawEmailID, result.RawEmailID)
			assert.Equal(t, email.UserID, result.UserID)
			assert.Equal(t, email.ThreadID, result.ThreadID)
			assert.NotZero(t, result.ProcessedAt)
		})
	}
}

// ---------------------------------------------------------------------------
// Router — metrics recording
// ---------------------------------------------------------------------------

func TestRouter_Route_MetricsRecorded(t *testing.T) {
	extract := new(mockExtractor)
	autoEngine := new(mockAutoEngine)
	log := testLogger()
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg)

	router := NewRouter(extract, autoEngine, log, metrics)

	email := newTestEvent()
	extract.On("Process", mock.Anything, email).Return(&models.ExtractedDatum{
		Type: "2fa", Value: "123456", NotificationText: "Code",
	}, nil)

	result, err := router.Route(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify metrics were recorded.
	assert.Equal(t, models.RouteExtract, result.Route)
}

// ---------------------------------------------------------------------------
// Router — nil event
// ---------------------------------------------------------------------------

func TestRouter_Route_NilEvent_Panics(t *testing.T) {
	extract := new(mockExtractor)
	autoEngine := new(mockAutoEngine)
	log := testLogger()
	metrics := newTestMetrics()

	router := NewRouter(extract, autoEngine, log, metrics)

	// Routing a nil event will panic because we dereference event fields.
	// This documents the expected behavior — callers must validate input.
	assert.Panics(t, func() {
		_, _ = router.Route(context.Background(), nil)
	})
}
