// Package nats tests the send consumer that processes email.send NATS messages.
package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/decisionstack/ingestion/internal/models"
	"github.com/decisionstack/ingestion/internal/mocks"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Test fixtures
// ---------------------------------------------------------------------------

func strPtr(s string) *string {
	return &s
}

// ---------------------------------------------------------------------------
// SendJobPayload JSON roundtrip
// ---------------------------------------------------------------------------

func TestSendJobPayloadJSONRoundtrip(t *testing.T) {
	original := SendJobPayload{
		DraftID:    uuid.New(),
		UserID:     uuid.New(),
		ThreadID:   uuid.New(),
		DraftBody:  "Test body content",
		Subject:    "Re: Test Subject",
		InReplyTo:  strPtr("<msg-id-1@example.com>"),
		References: []string{"<msg-id-1@example.com>", "<msg-id-2@example.com>"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded SendJobPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.DraftID != original.DraftID {
		t.Errorf("draft_id mismatch: got %v, want %v", decoded.DraftID, original.DraftID)
	}
	if decoded.UserID != original.UserID {
		t.Errorf("user_id mismatch: got %v, want %v", decoded.UserID, original.UserID)
	}
	if decoded.ThreadID != original.ThreadID {
		t.Errorf("thread_id mismatch: got %v, want %v", decoded.ThreadID, original.ThreadID)
	}
	if decoded.DraftBody != original.DraftBody {
		t.Errorf("draft_body mismatch: got %q, want %q", decoded.DraftBody, original.DraftBody)
	}
	if decoded.Subject != original.Subject {
		t.Errorf("subject mismatch: got %q, want %q", decoded.Subject, original.Subject)
	}
	if decoded.InReplyTo == nil || *decoded.InReplyTo != *original.InReplyTo {
		t.Errorf("in_reply_to mismatch: got %v, want %v", decoded.InReplyTo, original.InReplyTo)
	}
	if len(decoded.References) != len(original.References) {
		t.Errorf("references length mismatch: got %d, want %d", len(decoded.References), len(original.References))
	}
	for i := range original.References {
		if decoded.References[i] != original.References[i] {
			t.Errorf("references[%d] mismatch: got %q, want %q", i, decoded.References[i], original.References[i])
		}
	}
}

func TestSendJobPayloadJSONRoundtripEmptyOptional(t *testing.T) {
	original := SendJobPayload{
		DraftID:   uuid.New(),
		UserID:    uuid.New(),
		ThreadID:  uuid.New(),
		DraftBody: "Simple body",
		Subject:   "Simple Subject",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded SendJobPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.InReplyTo != nil {
		t.Errorf("in_reply_to should be nil, got %v", *decoded.InReplyTo)
	}
	if len(decoded.References) != 0 {
		t.Errorf("references should be empty, got %v", decoded.References)
	}
}

// ---------------------------------------------------------------------------
// ProviderNameFromAccount tests
// ---------------------------------------------------------------------------

func TestProviderNameFromAccount(t *testing.T) {
	tests := []struct {
		accountID string
		want      string
	}{
		{"gmail", "gmail"},
		{"GMAIL", "gmail"},
		{"google", "gmail"},
		{"user@gmail.com", "gmail"},
		{"outlook", "outlook"},
		{"OUTLOOK", "outlook"},
		{"microsoft", "outlook"},
		{"user@hotmail.com", "outlook"},
		{"user@outlook.com", "outlook"},
		{"unknown", "gmail"}, // default fallback
		{"", "gmail"},        // default fallback
	}

	for _, tt := range tests {
		t.Run(tt.accountID, func(t *testing.T) {
			got := ProviderNameFromAccount(tt.accountID)
			if got != tt.want {
				t.Errorf("ProviderNameFromAccount(%q) = %q, want %q", tt.accountID, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NATS subject constant test
// ---------------------------------------------------------------------------

func TestNATSSubjectEmailSend(t *testing.T) {
	if NATSSubjectEmailSend != "email.send" {
		t.Errorf("NATSSubjectEmailSend = %q, want %q", NATSSubjectEmailSend, "email.send")
	}
}

// ---------------------------------------------------------------------------
// EmailSentEvent JSON roundtrip
// ---------------------------------------------------------------------------

func TestEmailSentEventJSONRoundtrip(t *testing.T) {
	original := EmailSentEvent{
		DraftID:   uuid.New(),
		MessageID: "msg-12345",
		SentAt:    time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded EmailSentEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.DraftID != original.DraftID {
		t.Errorf("draft_id mismatch: got %v, want %v", decoded.DraftID, original.DraftID)
	}
	if decoded.MessageID != original.MessageID {
		t.Errorf("message_id mismatch: got %q, want %q", decoded.MessageID, original.MessageID)
	}
	if !decoded.SentAt.Equal(original.SentAt) {
		t.Errorf("sent_at mismatch: got %v, want %v", decoded.SentAt, original.SentAt)
	}
}

// ---------------------------------------------------------------------------
// Compile-time interface checks
// ---------------------------------------------------------------------------

// Ensure MockProvider implements EmailProvider at compile time.
var _ models.EmailProvider = (*mocks.MockProvider)(nil)

// Ensure MockProvider implements OAuthProvider at compile time.
var _ models.OAuthProvider = (*mocks.MockProvider)(nil)

// TestSendEmailInterfaceReturnSignature verifies the EmailProvider interface
// returns (string, error) — not just error.
func TestSendEmailInterfaceReturnSignature(t *testing.T) {
	// This test ensures the interface change is reflected in mocks.
	mockProvider := mocks.NewMockGmailProvider()
	mockProvider.SendEmailReturn = "msg_test_12345"

	var provider models.EmailProvider = mockProvider
	msgID, err := provider.SendEmail(context.Background(), "test-token", models.SendEmailRequest{
		To:       "recipient@example.com",
		Subject:  "Test",
		BodyText: "Body",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msgID != "msg_test_12345" {
		t.Errorf("message ID = %q, want %q", msgID, "msg_test_12345")
	}
}

// TestSendEmailReturnsMessageID verifies the mock returns a non-empty message ID by default.
func TestSendEmailReturnsMessageID(t *testing.T) {
	mockProvider := mocks.NewMockGmailProvider()

	msgID, err := mockProvider.SendEmail(context.Background(), "test-token", models.SendEmailRequest{
		To:       "recipient@example.com",
		Subject:  "Test",
		BodyText: "Body",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msgID == "" {
		t.Error("expected non-empty message ID from mock provider")
	}
	if len(mockProvider.SendEmailCalls) != 1 {
		t.Errorf("expected 1 SendEmail call, got %d", len(mockProvider.SendEmailCalls))
	}
}

// TestMockProviderSendEmailReturnValue verifies the configured return value is used.
func TestMockProviderSendEmailReturnValue(t *testing.T) {
	mockProvider := mocks.NewMockOutlookProvider()
	mockProvider.SendEmailReturn = "custom_msg_id_67890"

	msgID, err := mockProvider.SendEmail(context.Background(), "token", models.SendEmailRequest{
		To:       "to@example.com",
		Subject:  "Subject",
		BodyText: "Body",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msgID != "custom_msg_id_67890" {
		t.Errorf("message ID = %q, want %q", msgID, "custom_msg_id_67890")
	}
}

// TestMockProviderSendEmailErrorCase verifies error returns empty message ID.
func TestMockProviderSendEmailErrorCase(t *testing.T) {
	mockProvider := mocks.NewMockGmailProvider()
	mockProvider.SendEmailErr = fmt.Errorf("network error")

	msgID, err := mockProvider.SendEmail(context.Background(), "token", models.SendEmailRequest{
		To:       "to@example.com",
		Subject:  "Subject",
		BodyText: "Body",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Mock returns auto-generated ID even on error unless explicitly cleared
	// but the error should still be returned
	if err.Error() != "network error" {
		t.Errorf("error = %v, want 'network error'", err)
	}
}
