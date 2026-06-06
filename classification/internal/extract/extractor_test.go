package extract

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/decisionstack/classification/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

type mockRawEmailStore struct {
	mock.Mock
}

func (m *mockRawEmailStore) FetchBody(ctx context.Context, rawEmailID uuid.UUID) (string, string, error) {
	args := m.Called(ctx, rawEmailID)
	return args.String(0), args.String(1), args.Error(2)
}

type mockEventPublisher struct {
	mock.Mock
}

func (m *mockEventPublisher) PublishExtractCompleted(ctx context.Context, rawEmailID uuid.UUID, userID uuid.UUID, datum *models.ExtractedDatum) error {
	args := m.Called(ctx, rawEmailID, userID, datum)
	return args.Error(0)
}

type mockDeletionTimer struct {
	mock.Mock
}

func (m *mockDeletionTimer) ScheduleRawEmailDeletion(ctx context.Context, rawEmailID uuid.UUID) error {
	args := m.Called(ctx, rawEmailID)
	return args.Error(0)
}

// ---------------------------------------------------------------------------
// Test fixtures
// ---------------------------------------------------------------------------

func newTestEmail() *models.EmailIngestedEvent {
	return &models.EmailIngestedEvent{
		EventID:     uuid.Must(uuid.NewRandom()),
		UserID:      uuid.Must(uuid.NewRandom()),
		Source:      "gmail",
		AccountID:   uuid.Must(uuid.NewRandom()),
		ThreadID:    uuid.Must(uuid.NewRandom()),
		RawEmailID:  uuid.Must(uuid.NewRandom()),
		S3URI:       "s3://bucket/email",
		SenderEmail: "alice@example.com",
		ContactIDs:  []uuid.UUID{},
	}
}

// ---------------------------------------------------------------------------
// NewExtractor
// ---------------------------------------------------------------------------

func TestNewExtractor(t *testing.T) {
	store := new(mockRawEmailStore)
	pub := new(mockEventPublisher)
	timer := new(mockDeletionTimer)

	extractor := NewExtractor(store, pub, timer, nil)
	require.NotNil(t, extractor)
	assert.Nil(t, extractor.onnx)
}

// ---------------------------------------------------------------------------
// Process — nil email
// ---------------------------------------------------------------------------

func TestExtractor_Process_NilEmail(t *testing.T) {
	extractor := NewExtractor(nil, nil, nil, nil)
	result, err := extractor.Process(context.Background(), nil)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "email is nil")
}

// ---------------------------------------------------------------------------
// Process — raw email already deleted
// ---------------------------------------------------------------------------

func TestExtractor_Process_RawEmailDeleted(t *testing.T) {
	store := new(mockRawEmailStore)
	extractor := NewExtractor(store, nil, nil, nil)

	email := newTestEmail()
	store.On("FetchBody", mock.Anything, email.RawEmailID).Return("", "", sql.ErrNoRows)

	result, err := extractor.Process(context.Background(), email)
	require.NoError(t, err)
	assert.Nil(t, result)
	store.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Process — DB error (other than no rows)
// ---------------------------------------------------------------------------

func TestExtractor_Process_DBError(t *testing.T) {
	store := new(mockRawEmailStore)
	extractor := NewExtractor(store, nil, nil, nil)

	email := newTestEmail()
	dbErr := errors.New("connection refused")
	store.On("FetchBody", mock.Anything, email.RawEmailID).Return("", "", dbErr)

	result, err := extractor.Process(context.Background(), email)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "connection refused")
	store.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Process — regex match (2FA)
// ---------------------------------------------------------------------------

func TestExtractor_Process_RegexMatch_2FA(t *testing.T) {
	store := new(mockRawEmailStore)
	pub := new(mockEventPublisher)
	timer := new(mockDeletionTimer)
	extractor := NewExtractor(store, pub, timer, nil)

	email := newTestEmail()
	subject := "Your verification code"
	body := "Your verification code is 654321"
	store.On("FetchBody", mock.Anything, email.RawEmailID).Return(subject, body, nil)
	pub.On("PublishExtractCompleted", mock.Anything, email.RawEmailID, email.UserID, mock.AnythingOfType("*models.ExtractedDatum")).Return(nil)
	timer.On("ScheduleRawEmailDeletion", mock.Anything, email.RawEmailID).Return(nil)

	result, err := extractor.Process(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.RouteExtract, result.Route)
	assert.InDelta(t, 1.0, result.Confidence, 0.001)
	require.NotNil(t, result.ExtractedData)
	assert.Equal(t, "2fa", result.ExtractedData.Type)
	assert.Equal(t, "654321", result.ExtractedData.Value)

	store.AssertExpectations(t)
	pub.AssertExpectations(t)
	timer.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Process — regex match (tracking)
// ---------------------------------------------------------------------------

func TestExtractor_Process_RegexMatch_Tracking(t *testing.T) {
	store := new(mockRawEmailStore)
	pub := new(mockEventPublisher)
	timer := new(mockDeletionTimer)
	extractor := NewExtractor(store, pub, timer, nil)

	email := newTestEmail()
	subject := "Package shipped"
	body := "Your UPS tracking number is 1Z888BB20234567890"
	store.On("FetchBody", mock.Anything, email.RawEmailID).Return(subject, body, nil)
	pub.On("PublishExtractCompleted", mock.Anything, email.RawEmailID, email.UserID, mock.AnythingOfType("*models.ExtractedDatum")).Return(nil)
	timer.On("ScheduleRawEmailDeletion", mock.Anything, email.RawEmailID).Return(nil)

	result, err := extractor.Process(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.RouteExtract, result.Route)
	require.NotNil(t, result.ExtractedData)
	assert.Equal(t, "tracking", result.ExtractedData.Type)

	store.AssertExpectations(t)
	pub.AssertExpectations(t)
	timer.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Process — calendar MIME detection via S3 URI
// ---------------------------------------------------------------------------

func TestExtractor_Process_CalendarMIME(t *testing.T) {
	store := new(mockRawEmailStore)
	pub := new(mockEventPublisher)
	timer := new(mockDeletionTimer)
	extractor := NewExtractor(store, pub, timer, nil)

	email := newTestEmail()
	email.S3URI = "s3://bucket/invite.ics"
	store.On("FetchBody", mock.Anything, email.RawEmailID).Return("Meeting Invite", "some body", nil)
	pub.On("PublishExtractCompleted", mock.Anything, email.RawEmailID, email.UserID, mock.AnythingOfType("*models.ExtractedDatum")).Return(nil)
	timer.On("ScheduleRawEmailDeletion", mock.Anything, email.RawEmailID).Return(nil)

	result, err := extractor.Process(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.RouteExtract, result.Route)
	require.NotNil(t, result.ExtractedData)
	assert.Equal(t, "calendar", result.ExtractedData.Type)
	assert.Equal(t, "calendar_invite", result.ExtractedData.Value)

	store.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Process — no match (falls through)
// ---------------------------------------------------------------------------

func TestExtractor_Process_NoMatch_FallThrough(t *testing.T) {
	store := new(mockRawEmailStore)
	extractor := NewExtractor(store, nil, nil, nil)

	email := newTestEmail()
	store.On("FetchBody", mock.Anything, email.RawEmailID).Return("Hello", "Just a regular conversation email with nothing extractable.", nil)

	result, err := extractor.Process(context.Background(), email)
	require.NoError(t, err)
	assert.Nil(t, result)

	store.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Process — publisher error is non-fatal
// ---------------------------------------------------------------------------

func TestExtractor_Process_PublisherError_NonFatal(t *testing.T) {
	store := new(mockRawEmailStore)
	pub := new(mockEventPublisher)
	extractor := NewExtractor(store, pub, nil, nil)

	email := newTestEmail()
	store.On("FetchBody", mock.Anything, email.RawEmailID).Return("", "Your code is 112233", nil)
	pub.On("PublishExtractCompleted", mock.Anything, email.RawEmailID, email.UserID, mock.Anything).Return(errors.New("nats unavailable"))

	result, err := extractor.Process(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.RouteExtract, result.Route)

	store.AssertExpectations(t)
	pub.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Process — timer error is non-fatal
// ---------------------------------------------------------------------------

func TestExtractor_Process_TimerError_NonFatal(t *testing.T) {
	store := new(mockRawEmailStore)
	pub := new(mockEventPublisher)
	timer := new(mockDeletionTimer)
	extractor := NewExtractor(store, pub, timer, nil)

	email := newTestEmail()
	store.On("FetchBody", mock.Anything, email.RawEmailID).Return("", "Your code is 445566", nil)
	pub.On("PublishExtractCompleted", mock.Anything, email.RawEmailID, email.UserID, mock.Anything).Return(nil)
	timer.On("ScheduleRawEmailDeletion", mock.Anything, email.RawEmailID).Return(errors.New("redis down"))

	result, err := extractor.Process(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.RouteExtract, result.Route)

	store.AssertExpectations(t)
	pub.AssertExpectations(t)
	timer.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Process — nil dependencies (publisher and timer are optional)
// ---------------------------------------------------------------------------

func TestExtractor_Process_NilPublisherAndTimer(t *testing.T) {
	store := new(mockRawEmailStore)
	extractor := NewExtractor(store, nil, nil, nil)

	email := newTestEmail()
	store.On("FetchBody", mock.Anything, email.RawEmailID).Return("", "Your verification code is 778899", nil)

	result, err := extractor.Process(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, models.RouteExtract, result.Route)
	require.NotNil(t, result.ExtractedData)
	assert.Equal(t, "2fa", result.ExtractedData.Type)

	store.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Process — ONNX nil skips ML path
// ---------------------------------------------------------------------------

func TestExtractor_Process_ONNXNil_SkipsML(t *testing.T) {
	store := new(mockRawEmailStore)
	extractor := NewExtractor(store, nil, nil, nil) // onnx is nil

	email := newTestEmail()
	// Body won't match regex but ONNX is nil so it falls through.
	store.On("FetchBody", mock.Anything, email.RawEmailID).Return("Newsletter", "Weekly digest with no extractable patterns.", nil)

	result, err := extractor.Process(context.Background(), email)
	require.NoError(t, err)
	assert.Nil(t, result)

	store.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// buildONNXInput
// ---------------------------------------------------------------------------

func TestBuildONNXInput(t *testing.T) {
	subject := "Invoice from Vendor"
	body := "This is the body of the email. It contains various information about your invoice."

	input := buildONNXInput(subject, body)
	assert.Contains(t, input, subject)
	assert.Contains(t, input, "This is the body")
}

func TestBuildONNXInput_TruncatesBody(t *testing.T) {
	subject := "Test"
	// Body longer than 200 chars.
	var longBody string
	for i := 0; i < 50; i++ {
		longBody += "word "
	}

	input := buildONNXInput(subject, longBody)
	assert.LessOrEqual(t, len(input), len(subject)+1+200+10) // subject + space + 200 + some buffer
	assert.Contains(t, input, subject)
}

// ---------------------------------------------------------------------------
// onnxClassToDatum
// ---------------------------------------------------------------------------

func TestOnnxClassToDatum(t *testing.T) {
	tests := []struct {
		class       string
		confidence  float64
		wantType    string
		wantNotifTmpl string
	}{
		{classReceipt, 0.97, "receipt", "Receipt or order confirmation"},
		{classNewsletter, 0.96, "newsletter", "Newsletter received"},
		{classNotification, 0.98, "notification", "Notification received"},
		{"unknown_class", 0.50, "notification", "Notification received"},
	}

	for _, tt := range tests {
		t.Run(tt.class, func(t *testing.T) {
			datum := onnxClassToDatum(tt.class, tt.confidence)
			require.NotNil(t, datum)
			assert.Equal(t, tt.wantType, datum.Type)
			assert.Equal(t, tt.wantNotifTmpl, datum.NotificationText)
			assert.Contains(t, datum.Value, tt.class)
		})
	}
}

// ---------------------------------------------------------------------------
// ClassificationResult fields
// ---------------------------------------------------------------------------

func TestExtractor_Process_ResultFields(t *testing.T) {
	store := new(mockRawEmailStore)
	pub := new(mockEventPublisher)
	extractor := NewExtractor(store, pub, nil, nil)

	email := newTestEmail()
	store.On("FetchBody", mock.Anything, email.RawEmailID).Return("", "Order #XYZ-789", nil)
	pub.On("PublishExtractCompleted", mock.Anything, email.RawEmailID, email.UserID, mock.Anything).Return(nil)

	result, err := extractor.Process(context.Background(), email)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, email.RawEmailID, result.RawEmailID)
	assert.Equal(t, email.UserID, result.UserID)
	assert.Equal(t, email.ThreadID, result.ThreadID)
	assert.NotZero(t, result.ProcessedAt)
}
