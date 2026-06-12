package extract

import (
	"testing"

	"github.com/decisionstack/classification/internal/models"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Pattern compilation sanity check
// ---------------------------------------------------------------------------

func TestPatternCount(t *testing.T) {
	count := PatternCount()
	require.Greater(t, count, 0, "expected at least one compiled pattern")
	// Should have patterns for 2FA, tracking, and receipts.
	assert.GreaterOrEqual(t, count, 5, "expected at least 5 compiled patterns (2FA + tracking + receipt)")
}

func TestPatternNames(t *testing.T) {
	names := PatternNames()
	require.NotEmpty(t, names)

	// Check expected patterns exist.
	expected := []string{"2fa_6digit", "2fa_4digit", "ups_tracking", "order_number_hash"}
	for _, exp := range expected {
		found := false
		for _, name := range names {
			if name == exp {
				found = true
				break
			}
		}
		assert.True(t, found, "expected pattern %q to be compiled", exp)
	}
}

// ---------------------------------------------------------------------------
// 2FA code extraction
// ---------------------------------------------------------------------------

func TestExtract2FA(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantType string
		wantVal  string
	}{
		{
			name:     "6-digit code",
			body:     "Your verification code is 123456",
			wantType: "2fa",
			wantVal:  "123456",
		},
		{
			name:     "6-digit code with prefix text",
			body:     "Here is your OTP: 987654. It expires in 5 minutes.",
			wantType: "2fa",
			wantVal:  "987654",
		},
		{
			name:     "4-digit pin",
			body:     "Your PIN is 9876",
			wantType: "2fa",
			wantVal:  "9876",
		},
		{
			name:     "5-digit token",
			body:     "Your access token: 54321",
			wantType: "2fa",
			wantVal:  "54321",
		},
		{
			name:     "8-digit code",
			body:     "Verification code: 12345678",
			wantType: "2fa",
			wantVal:  "12345678",
		},
		{
			name:     "alphanumeric code",
			body:     "Your code is AB12CD",
			wantType: "2fa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email := &models.EmailIngestedEvent{
				RawEmailID: uuid.Must(uuid.NewRandom()),
				S3URI:      "s3://test/bucket",
			}
			datum, ok := Extract(email, "", tt.body)
			require.True(t, ok, "expected a match")
			require.NotNil(t, datum)
			assert.Equal(t, tt.wantType, datum.Type, "expected type %q, got %q", tt.wantType, datum.Type)
			if tt.wantVal != "" {
				assert.Equal(t, tt.wantVal, datum.Value, "expected value %q, got %q", tt.wantVal, datum.Value)
			}
			assert.NotEmpty(t, datum.NotificationText)
		})
	}
}

// ---------------------------------------------------------------------------
// Tracking number extraction
// ---------------------------------------------------------------------------

func TestExtractTracking(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantType string
		wantVal  string
	}{
		{
			name:     "UPS tracking",
			body:     "Track your package: 1Z999AA10123456784",
			wantType: "tracking",
			wantVal:  "1Z999AA10123456784",
		},
		{
			name:     "FedEx tracking 12-digit",
			body:     "Your FedEx tracking number is 123456789012",
			wantType: "tracking",
			wantVal:  "123456789012",
		},
		{
			name:     "FedEx tracking 14-digit",
			body:     "Track: 12345678901234",
			wantType: "tracking",
			wantVal:  "12345678901234",
		},
		{
			name:     "USPS 22-digit",
			body:     "USPS tracking: 1234567890123456789012",
			wantType: "tracking",
		},
		{
			name:     "USPS 13-digit",
			body:     "Your package EA123456789US is on its way",
			wantType: "tracking",
			wantVal:  "EA123456789US",
		},
		{
			name:     "DHL tracking",
			body:     "DHL tracking: 1234567890",
			wantType: "tracking",
			wantVal:  "1234567890",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email := &models.EmailIngestedEvent{
				RawEmailID: uuid.Must(uuid.NewRandom()),
				S3URI:      "s3://test/bucket",
			}
			datum, ok := Extract(email, "", tt.body)
			require.True(t, ok, "expected a match for %q", tt.body)
			require.NotNil(t, datum)
			assert.Equal(t, tt.wantType, datum.Type)
			if tt.wantVal != "" {
				assert.Equal(t, tt.wantVal, datum.Value)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Receipt / order extraction
// ---------------------------------------------------------------------------

func TestExtractReceipt(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantType string
	}{
		{
			name:     "order with hash",
			body:     "Order #ABC-12345 has been confirmed",
			wantType: "receipt",
		},
		{
			name:     "order number label",
			body:     "Your order number INV-2024-001 is ready",
			wantType: "receipt",
		},
		{
			name:     "receipt with dollar total",
			body:     "Total amount: $123.45",
			wantType: "receipt",
		},
		{
			name:     "receipt with euro total",
			body:     "Total amount: €99.99",
			wantType: "receipt",
		},
		{
			name:     "subtotal",
			body:     "Subtotal: $45.67",
			wantType: "receipt",
		},
		{
			name:     "invoice with hash",
			body:     "Invoice #INV-789 for your purchase",
			wantType: "receipt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email := &models.EmailIngestedEvent{
				RawEmailID: uuid.Must(uuid.NewRandom()),
				S3URI:      "s3://test/bucket",
			}
			datum, ok := Extract(email, "", tt.body)
			require.True(t, ok, "expected a match")
			require.NotNil(t, datum)
			assert.Equal(t, tt.wantType, datum.Type)
			assert.NotEmpty(t, datum.Value)
			assert.NotEmpty(t, datum.NotificationText)
		})
	}
}

// ---------------------------------------------------------------------------
// Calendar MIME detection (via S3 URI and subject)
// ---------------------------------------------------------------------------

func TestExtractCalendarMIME(t *testing.T) {
	tests := []struct {
		name    string
		s3URI   string
		subject string
		wantOk  bool
	}{
		{
			name:    "ics file in S3 URI",
			s3URI:   "s3://bucket/emails/invite.ics",
			subject: "Meeting tomorrow",
			wantOk:  true,
		},
		{
			name:    "ical extension",
			s3URI:   "s3://bucket/event.ical",
			subject: "Team sync",
			wantOk:  true,
		},
		{
			name:    "subject invitation",
			s3URI:   "s3://bucket/email123",
			subject: "Invitation: Q2 Planning",
			wantOk:  true,
		},
		{
			name:    "subject accepted",
			s3URI:   "s3://bucket/email456",
			subject: "Accepted: Weekly Standup",
			wantOk:  true,
		},
		{
			name:    "subject tentative",
			s3URI:   "s3://bucket/email789",
			subject: "Tentative: Review Meeting",
			wantOk:  true,
		},
		{
			name:    "subject declined",
			s3URI:   "s3://bucket/email000",
			subject: "Declined: Optional Sync",
			wantOk:  true,
		},
		{
			name:    "subject updated invitation",
			s3URI:   "s3://bucket/email111",
			subject: "Updated invitation: Board Meeting",
			wantOk:  true,
		},
		{
			name:    "subject canceled",
			s3URI:   "s3://bucket/email222",
			subject: "Canceled: Standup",
			wantOk:  true,
		},
		{
			name:    "not calendar",
			s3URI:   "s3://bucket/email333",
			subject: "Regular email subject",
			wantOk:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email := &models.EmailIngestedEvent{
				RawEmailID: uuid.Must(uuid.NewRandom()),
				S3URI:      tt.s3URI,
			}
			datum, ok := Extract(email, tt.subject, "some body text")
			if tt.wantOk {
				require.True(t, ok, "expected calendar match")
				require.NotNil(t, datum)
				assert.Equal(t, "calendar", datum.Type)
				assert.Equal(t, "calendar_invite", datum.Value)
			} else {
				// May match other patterns or not at all.
				if ok {
					assert.NotEqual(t, "calendar", datum.Type, "should not detect calendar")
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Priority ordering — 2FA (10) should beat tracking (8) and receipt (7)
// ---------------------------------------------------------------------------

func TestExtract_Priority_2FAOverTracking(t *testing.T) {
	// Body contains both a tracking number AND a 2FA code.
	// 2FA has priority 10, tracking has priority 8.
	body := "Your UPS tracking number is 1Z999AA10123456784 and your verification code is 555666"
	email := &models.EmailIngestedEvent{
		RawEmailID: uuid.Must(uuid.NewRandom()),
		S3URI:      "s3://test/bucket",
	}

	datum, ok := Extract(email, "", body)
	require.True(t, ok)
	require.NotNil(t, datum)
	assert.Equal(t, "2fa", datum.Type, "2FA should win over tracking due to higher priority")
}

func TestExtract_Priority_TrackingOverReceipt(t *testing.T) {
	// Body contains both an order number and a tracking number.
	// Tracking has priority 8, receipt has priority 7.
	body := "Order #ABC-12345. Track: 1Z999AA10123456784"
	email := &models.EmailIngestedEvent{
		RawEmailID: uuid.Must(uuid.NewRandom()),
		S3URI:      "s3://test/bucket",
	}

	datum, ok := Extract(email, "", body)
	require.True(t, ok)
	require.NotNil(t, datum)
	assert.Equal(t, "tracking", datum.Type, "tracking should win over receipt due to higher priority")
}

// ---------------------------------------------------------------------------
// No match cases
// ---------------------------------------------------------------------------

func TestExtract_NoMatch(t *testing.T) {
	bodies := []string{
		"Hello, how are you?",
		"Just checking in to see if you're available for a meeting next week.",
		"Can you review this document when you have a chance?",
		"Thanks for your help with the project!",
	}

	for _, body := range bodies {
		email := &models.EmailIngestedEvent{
			RawEmailID: uuid.Must(uuid.NewRandom()),
			S3URI:      "s3://test/bucket",
		}
		datum, ok := Extract(email, "", body)
		assert.False(t, ok, "should not match for body: %q", body)
		assert.Nil(t, datum)
	}
}

// ---------------------------------------------------------------------------
// Subject included in scan
// ---------------------------------------------------------------------------

func TestExtract_SubjectIncluded(t *testing.T) {
	// The subject alone should be scanned.
	email := &models.EmailIngestedEvent{
		RawEmailID: uuid.Must(uuid.NewRandom()),
		S3URI:      "s3://test/bucket",
	}
	datum, ok := Extract(email, "Your verification code is 777888", "")
	require.True(t, ok)
	require.NotNil(t, datum)
	assert.Equal(t, "2fa", datum.Type)
	assert.Equal(t, "777888", datum.Value)
}

// ---------------------------------------------------------------------------
// normalizeInput truncation
// ---------------------------------------------------------------------------

func TestNormalizeInput_Truncation(t *testing.T) {
	// Build a body longer than 2048 chars (410 * 5 = 2050 chars before the code).
	var longBody string
	for i := 0; i < 410; i++ {
		longBody += "word "
	}
	// Add a 2FA code at the very end (past 2048 chars).
	longBody += " verification code is 999888"

	email := &models.EmailIngestedEvent{
		RawEmailID: uuid.Must(uuid.NewRandom()),
		S3URI:      "s3://test/bucket",
	}

	// Should not find the code past 2048 chars.
	datum, ok := Extract(email, "", longBody)
	assert.False(t, ok, "code past 2048 chars should not be found")
	assert.Nil(t, datum)
}

func TestNormalizeInput_EmptyBody(t *testing.T) {
	email := &models.EmailIngestedEvent{
		RawEmailID: uuid.Must(uuid.NewRandom()),
		S3URI:      "s3://test/bucket",
	}
	datum, ok := Extract(email, "", "")
	assert.False(t, ok, "empty input should not match")
	assert.Nil(t, datum)
}

// ---------------------------------------------------------------------------
// buildDatum notification text
// ---------------------------------------------------------------------------

func TestBuildDatum_NotificationTemplates(t *testing.T) {
	tests := []struct {
		inputType ExtractType
		wantTmpl  string
	}{
		{Type2FA, "Verification code detected"},
		{TypeTracking, "Package tracking update"},
		{TypeCalendar, "Calendar event received"},
		{TypeReceipt, "Receipt or order confirmation"},
		{TypeNewsletter, "Newsletter received"},
		{TypeNotification, "Notification received"},
	}

	for _, tt := range tests {
		t.Run(string(tt.inputType), func(t *testing.T) {
			cand := &matchCandidate{
				type_:    tt.inputType,
				pattern:  "test_pattern",
				priority: 10,
				value:    "test-value",
			}
			datum := buildDatum(cand)
			require.NotNil(t, datum)
			assert.Equal(t, tt.wantTmpl, datum.NotificationText)
			assert.Equal(t, "test-value", datum.Value)
		})
	}
}

// ---------------------------------------------------------------------------
// pickCaptureGroup
// ---------------------------------------------------------------------------

func TestPickCaptureGroup_FirstNonEmpty(t *testing.T) {
	// Full match + two capture groups, first empty.
	groups := []string{"full match", "", "captured"}
	got := pickCaptureGroup(groups)
	assert.Equal(t, "captured", got)
}

func TestPickCaptureGroup_NoCaptures(t *testing.T) {
	// Only the full match, no captures.
	groups := []string{"full match only"}
	got := pickCaptureGroup(groups)
	assert.Equal(t, "full match only", got)
}

func TestPickCaptureGroup_FirstCapture(t *testing.T) {
	groups := []string{"full", "first", "second"}
	got := pickCaptureGroup(groups)
	assert.Equal(t, "first", got)
}
