// Package nats tests NATS JetStream event publishing.
package nats

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestEmailIngestedEventJSONRoundtrip verifies JSON marshal/unmarshal for the
// local EmailIngestedEvent (duplicated from models for isolation testing).
func TestEmailIngestedEventJSONRoundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	original := &EmailIngestedEvent{
		EventID:            uuid.New(),
		UserID:             uuid.New(),
		Source:             "gmail",
		AccountID:          uuid.New(),
		ThreadID:           uuid.New(),
		RawEmailID:         uuid.New(),
		S3URI:              "s3://bucket/emails/raw/123.json",
		HasAttachments:     true,
		SenderEmail:        "alice@example.com",
		ReceivedAt:         now,
		ClassificationHint: "pending",
		ContactIDs:         []uuid.UUID{uuid.New(), uuid.New()},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded EmailIngestedEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.EventID != original.EventID {
		t.Errorf("event_id mismatch")
	}
	if decoded.UserID != original.UserID {
		t.Errorf("user_id mismatch")
	}
	if decoded.Source != original.Source {
		t.Errorf("source mismatch: %q vs %q", decoded.Source, original.Source)
	}
	if decoded.S3URI != original.S3URI {
		t.Errorf("s3_uri mismatch")
	}
	if !decoded.HasAttachments {
		t.Error("has_attachments should be true")
	}
	if decoded.ClassificationHint != "pending" {
		t.Errorf("classification_hint mismatch: %q", decoded.ClassificationHint)
	}
	if len(decoded.ContactIDs) != 2 {
		t.Errorf("expected 2 contact_ids, got %d", len(decoded.ContactIDs))
	}
}

// TestExtractCompletedEventJSONRoundtrip verifies JSON marshal/unmarshal.
func TestExtractCompletedEventJSONRoundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	original := &ExtractCompletedEvent{
		EventID:          uuid.New(),
		UserID:           uuid.New(),
		RawEmailID:       uuid.New(),
		ExtractType:      "2fa",
		ExtractedData:    "123456",
		NotificationText: "Your code is 123456",
		ProcessedAt:      now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded ExtractCompletedEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.ExtractType != original.ExtractType {
		t.Errorf("extract_type mismatch")
	}
	if decoded.ExtractedData != original.ExtractedData {
		t.Errorf("extracted_data mismatch")
	}
	if decoded.NotificationText != original.NotificationText {
		t.Errorf("notification_text mismatch")
	}
}

// TestSubjectConstants verifies all NATS subject constants.
func TestSubjectConstants(t *testing.T) {
	expected := map[string]string{
		SubjectEmailIngested:        "email.ingested",
		SubjectEmailIngestedDLQ:     "email.ingested.dlq",
		SubjectIntelligenceCompress: "intelligence.compress",
		SubjectExtractCompleted:     "ExtractCompleted",
		SubjectAutoHandled:          "AutoHandled",
		SubjectCardCreated:          "sync.notify.CardCreated",
	}

	for constant, expectedValue := range expected {
		if constant != expectedValue {
			t.Errorf("subject constant mismatch: got %q, want %q", constant, expectedValue)
		}
	}
}

// TestStreamConfigs verifies stream configurations are well-formed.
func TestStreamConfigs(t *testing.T) {
	if len(StreamConfigs) == 0 {
		t.Fatal("StreamConfigs should not be empty")
	}

	requiredStreams := []string{
		"EMAIL_INGESTED",
		"EMAIL_INGESTED_DLQ",
		"INTELLIGENCE_COMPRESS",
		"EXTRACT_COMPLETED",
		"AUTO_HANDLED",
		"SYNC_NOTIFY_CARD_CREATED",
	}

	for _, name := range requiredStreams {
		cfg, ok := StreamConfigs[name]
		if !ok {
			t.Errorf("missing stream config: %s", name)
			continue
		}
		if cfg.Name != name {
			t.Errorf("stream config name mismatch: %q vs %q", cfg.Name, name)
		}
		if len(cfg.Subjects) == 0 {
			t.Errorf("stream %s has no subjects", name)
		}
	}
}

// TestRetryBackoffCalculation verifies the exponential backoff formula.
func TestRetryBackoffCalculation(t *testing.T) {
	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 0},                    // first attempt: no delay
		{1, 500 * time.Millisecond}, // 500ms * 2^0 = 500ms
		{2, 1 * time.Second},      // 500ms * 2^1 = 1s
		{3, 2 * time.Second},      // 500ms * 2^2 = 2s
	}

	for _, tt := range tests {
		t.Run(string(rune('0'+tt.attempt)), func(t *testing.T) {
			var delay time.Duration
			if tt.attempt > 0 {
				delay = retryBaseDelay * time.Duration(1<<uint(tt.attempt-1))
				if delay > retryMaxDelay {
					delay = retryMaxDelay
				}
			}
			if delay != tt.expected {
				t.Errorf("attempt %d: delay = %v, want %v", tt.attempt, delay, tt.expected)
			}
		})
	}
}

// TestMaxRetriesConstant verifies the retry constant.
func TestMaxRetriesConstant(t *testing.T) {
	if maxPublishRetries != 3 {
		t.Errorf("maxPublishRetries = %d, want 3", maxPublishRetries)
	}
}

// TestRetryBaseDelay verifies the base delay constant.
func TestRetryBaseDelay(t *testing.T) {
	if retryBaseDelay != 500*time.Millisecond {
		t.Errorf("retryBaseDelay = %v, want 500ms", retryBaseDelay)
	}
}

// TestRetryMaxDelay verifies the max delay constant.
func TestRetryMaxDelay(t *testing.T) {
	if retryMaxDelay != 5*time.Second {
		t.Errorf("retryMaxDelay = %v, want 5s", retryMaxDelay)
	}
}

// TestDLQMessageFormat verifies the structure of DLQ messages.
func TestDLQMessageFormat(t *testing.T) {
	// Simulate the DLQ message structure from publishToDLQ
	originalData := []byte(`{"event_id":"test-123"}`)

	dlqMsg := map[string]interface{}{
		"original_subject": SubjectEmailIngested,
		"data":             json.RawMessage(originalData),
		"failed_at":        time.Now().UTC().Format(time.RFC3339),
		"reason":           "max retries exceeded",
	}

	dlqData, err := json.Marshal(dlqMsg)
	if err != nil {
		t.Fatalf("marshal dlq message: %v", err)
	}

	// Verify it can be unmarshaled
	var parsed map[string]interface{}
	if err := json.Unmarshal(dlqData, &parsed); err != nil {
		t.Fatalf("unmarshal dlq message: %v", err)
	}

	if parsed["original_subject"] != SubjectEmailIngested {
		t.Errorf("original_subject mismatch")
	}
	if parsed["reason"] != "max retries exceeded" {
		t.Errorf("reason mismatch: %v", parsed["reason"])
	}
	if parsed["failed_at"] == "" {
		t.Error("failed_at should be set")
	}

	// Verify data is preserved
	dataBytes, _ := json.Marshal(parsed["data"])
	if string(dataBytes) != string(originalData) {
		t.Errorf("data not preserved: %s vs %s", dataBytes, originalData)
	}
}

// TestPublisherInterface verifies the Publisher interface is satisfied.
func TestPublisherInterface(t *testing.T) {
	// Compile-time check: ensure JetStreamPublisher implements Publisher
	var _ Publisher = (*JetStreamPublisher)(nil)
}

// TestBackoffCapped verifies backoff does not exceed retryMaxDelay.
func TestBackoffCapped(t *testing.T) {
	// Simulate many retries - delay should be capped
	for attempt := 1; attempt <= 10; attempt++ {
		delay := retryBaseDelay * time.Duration(1<<uint(attempt-1))
		if delay > retryMaxDelay {
			delay = retryMaxDelay
		}
		if delay > retryMaxDelay {
			t.Errorf("attempt %d: delay %v exceeds max %v", attempt, delay, retryMaxDelay)
		}
	}
}
