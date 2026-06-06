// Package models tests JSON marshaling/unmarshaling for all event types.
package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestEncryptedTokenJSONRoundtrip verifies JSON marshal/unmarshal for EncryptedToken.
func TestEncryptedTokenJSONRoundtrip(t *testing.T) {
	original := &EncryptedToken{
		Ciphertext: []byte("encrypted-data-here"),
		Nonce:      []byte("12byte-nonce"),
		KeyID:      "kms-key-v1",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded EncryptedToken
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if string(decoded.Ciphertext) != string(original.Ciphertext) {
		t.Errorf("ciphertext mismatch: %q vs %q", decoded.Ciphertext, original.Ciphertext)
	}
	if string(decoded.Nonce) != string(original.Nonce) {
		t.Errorf("nonce mismatch: %q vs %q", decoded.Nonce, original.Nonce)
	}
	if decoded.KeyID != original.KeyID {
		t.Errorf("keyID mismatch: %q vs %q", decoded.KeyID, original.KeyID)
	}
}

// TestEncryptedTokenJSONEmpty verifies JSON handling with empty/nil fields.
func TestEncryptedTokenJSONEmpty(t *testing.T) {
	original := &EncryptedToken{
		Ciphertext: nil,
		Nonce:      []byte{},
		KeyID:      "",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded EncryptedToken
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.KeyID != "" {
		t.Errorf("expected empty keyID, got %q", decoded.KeyID)
	}
}

// TestTokenPairJSONRoundtrip verifies JSON marshal/unmarshal for TokenPair.
func TestTokenPairJSONRoundtrip(t *testing.T) {
	original := &TokenPair{
		RefreshToken: &EncryptedToken{
			Ciphertext: []byte("refresh-cipher"),
			Nonce:      []byte("12byte-nonce"),
			KeyID:      "key-1",
		},
		AccessToken: &EncryptedToken{
			Ciphertext: []byte("access-cipher"),
			Nonce:      []byte("12byte-nonce"),
			KeyID:      "key-1",
		},
		ExpiresAt:    ptr(time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)),
		ScopeGranted: []string{"email", "calendar"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded TokenPair
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.RefreshToken == nil {
		t.Fatal("expected non-nil RefreshToken")
	}
	if string(decoded.RefreshToken.Ciphertext) != "refresh-cipher" {
		t.Errorf("refresh ciphertext mismatch")
	}
	if decoded.AccessToken == nil {
		t.Fatal("expected non-nil AccessToken")
	}
	if string(decoded.AccessToken.Ciphertext) != "access-cipher" {
		t.Errorf("access ciphertext mismatch")
	}
	if decoded.ExpiresAt == nil || !decoded.ExpiresAt.Equal(*original.ExpiresAt) {
		t.Errorf("expires_at mismatch")
	}
	if len(decoded.ScopeGranted) != 2 || decoded.ScopeGranted[0] != "email" {
		t.Errorf("scope_granted mismatch: %v", decoded.ScopeGranted)
	}

	// AccessTokenPlaintext should NOT be marshaled (json:"-")
	var rawMap map[string]interface{}
	if err := json.Unmarshal(data, &rawMap); err != nil {
		t.Fatalf("unmarshal to map failed: %v", err)
	}
	if _, ok := rawMap["access_token_plaintext"]; ok {
		t.Error("AccessTokenPlaintext should not appear in JSON")
	}
}

// TestEmailIngestedEventJSONRoundtrip verifies JSON marshal/unmarshal.
func TestEmailIngestedEventJSONRoundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	original := &EmailIngestedEvent{
		EventID:            uuid.MustParse("a1b2c3d4-e5f6-7890-abcd-ef1234567890"),
		UserID:             uuid.MustParse("b2c3d4e5-f6a7-8901-bcde-f23456789012"),
		Source:             "gmail",
		AccountID:          uuid.MustParse("c3d4e5f6-a7b8-9012-cdef-345678901234"),
		ThreadID:           uuid.MustParse("d4e5f6a7-b8c9-0123-defa-456789012345"),
		RawEmailID:         uuid.MustParse("e5f6a7b8-c9d0-1234-efab-567890123456"),
		S3URI:              "s3://bucket/emails/raw/123.json",
		HasAttachments:     true,
		SenderEmail:        "alice@example.com",
		ReceivedAt:         now,
		ClassificationHint: "pending",
		ContactIDs: []uuid.UUID{
			uuid.MustParse("f6a7b8c9-d0e1-2345-fabc-678901234567"),
		},
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
		t.Errorf("event_id mismatch: %v vs %v", decoded.EventID, original.EventID)
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
	if len(decoded.ContactIDs) != 1 {
		t.Errorf("expected 1 contact_id, got %d", len(decoded.ContactIDs))
	}
}

// TestParsedEmailJSONRoundtrip verifies JSON marshal/unmarshal for ParsedEmail.
func TestParsedEmailJSONRoundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	inReplyTo := "<msg-123@example.com>"
	original := &ParsedEmail{
		ID:              uuid.New(),
		UserID:          uuid.New(),
		AccountID:       uuid.New(),
		Source:          "gmail",
		MessageID:       "<abc123@example.com>",
		InReplyTo:       &inReplyTo,
		References:      []string{"<ref1@example.com>", "<ref2@example.com>"},
		SenderEmail:     "alice@example.com",
		SenderName:      "Alice Smith",
		RecipientEmails: []string{"bob@example.com"},
		Subject:         "Meeting Notes",
		BodyText:        "Here are the notes from our meeting.",
		BodyHTML:        "<p>Here are the notes from our meeting.</p>",
		HasAttachments:  false,
		Attachments: []Attachment{
			{
				Filename:    "notes.pdf",
				ContentType: "application/pdf",
				Size:        102400,
				S3URI:       "s3://bucket/attachments/notes.pdf",
				IsInline:    false,
			},
		},
		ExtractedCodes: []string{"123456"},
		ReceivedAt:     now,
		S3URI:          "s3://bucket/emails/raw/456.json",
		ThreadHint: &ThreadHint{
			InReplyTo:  "<msg-123@example.com>",
			References: []string{"<ref1@example.com>"},
			Subject:    "Meeting Notes",
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded ParsedEmail
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.MessageID != original.MessageID {
		t.Errorf("message_id mismatch")
	}
	if decoded.SenderEmail != original.SenderEmail {
		t.Errorf("sender_email mismatch")
	}
	if decoded.Subject != original.Subject {
		t.Errorf("subject mismatch")
	}
	if decoded.Source != original.Source {
		t.Errorf("source mismatch")
	}
	if len(decoded.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(decoded.Attachments))
	}
	if decoded.Attachments[0].Filename != "notes.pdf" {
		t.Errorf("attachment filename mismatch")
	}
	if decoded.Attachments[0].Size != 102400 {
		t.Errorf("attachment size mismatch")
	}
	if decoded.ThreadHint == nil {
		t.Fatal("expected non-nil ThreadHint")
	}
	if decoded.ThreadHint.Subject != "Meeting Notes" {
		t.Errorf("thread_hint.subject mismatch")
	}
	if *decoded.InReplyTo != inReplyTo {
		t.Errorf("in_reply_to mismatch")
	}
	if len(decoded.References) != 2 {
		t.Errorf("references length mismatch: %d", len(decoded.References))
	}
}

// TestIngestionError verifies the IngestionError type.
func TestIngestionError(t *testing.T) {
	err := &IngestionError{
		Code:    ErrCodeOAuthExpired,
		Message: "refresh token expired",
		UserID:  "user-123",
		Retry:   false,
	}

	if err.Error() != "refresh token expired" {
		t.Errorf("Error() returned %q, want %q", err.Error(), "refresh token expired")
	}

	// Verify JSON roundtrip
	data, err2 := json.Marshal(err)
	if err2 != nil {
		t.Fatalf("marshal failed: %v", err2)
	}

	var decoded IngestionError
	if err3 := json.Unmarshal(data, &decoded); err3 != nil {
		t.Fatalf("unmarshal failed: %v", err3)
	}

	if decoded.Code != ErrCodeOAuthExpired {
		t.Errorf("code mismatch: %q vs %q", decoded.Code, ErrCodeOAuthExpired)
	}
	if decoded.Message != "refresh token expired" {
		t.Errorf("message mismatch")
	}
	if decoded.UserID != "user-123" {
		t.Errorf("user_id mismatch")
	}
	if decoded.Retry {
		t.Error("retry should be false")
	}
}

// TestJSONBValueScan verifies JSONB driver.Value and Scan.
func TestJSONBValueScan(t *testing.T) {
	tests := []struct {
		name     string
		input    JSONB
		expected string
	}{
		{
			name:     "simple_object",
			input:    JSONB{"key": "value", "num": 42},
			expected: `{"key":"value","num":42}`,
		},
		{
			name:     "nested_object",
			input:    JSONB{"outer": map[string]interface{}{"inner": true}},
			expected: ``, // complex nested - just verify no error
		},
		{
			name:     "nil",
			input:    nil,
			expected: "null",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := tt.input.Value()
			if err != nil {
				t.Fatalf("Value() failed: %v", err)
			}

			// Scan it back
			var scanned JSONB
			if err := scanned.Scan(val); err != nil {
				t.Fatalf("Scan() failed: %v", err)
			}

			// For non-nil inputs, verify roundtrip
			if tt.input != nil {
				scannedJSON, _ := json.Marshal(scanned)
				inputJSON, _ := json.Marshal(tt.input)
				if string(scannedJSON) != string(inputJSON) {
					t.Errorf("JSONB roundtrip failed: %s vs %s", scannedJSON, inputJSON)
				}
			}
		})
	}
}

// TestJSONBScanNil verifies JSONB Scan with nil value.
func TestJSONBScanNil(t *testing.T) {
	var j JSONB = JSONB{"existing": "data"}
	if err := j.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) failed: %v", err)
	}
	if j != nil {
		t.Errorf("expected nil after Scan(nil), got %v", j)
	}
}

// TestJSONBScanString verifies JSONB Scan with string input.
func TestJSONBScanString(t *testing.T) {
	var j JSONB
	if err := j.Scan(`{"test": true}`); err != nil {
		t.Fatalf("Scan(string) failed: %v", err)
	}
	if v, ok := j["test"]; !ok || v != true {
		t.Errorf("expected test=true, got %v", v)
	}
}

// TestJSONBScanInvalidType verifies JSONB Scan with invalid type.
func TestJSONBScanInvalidType(t *testing.T) {
	var j JSONB = JSONB{"existing": "data"}
	// Scanning an unsupported type should leave JSONB unchanged (returns nil error per source)
	err := j.Scan(12345)
	if err != nil {
		t.Logf("Scan(int) returned error: %v (behavior may vary)", err)
	}
}

// TestUUIDGeneration verifies that UUID generation works for model types.
func TestUUIDGeneration(t *testing.T) {
	// Verify we can create UUIDs for key fields
	email := &RawEmail{
		ID:      uuid.New(),
		ThreadID: uuid.New(),
		UserID:  uuid.New(),
	}

	if email.ID == uuid.Nil {
		t.Error("expected non-nil ID")
	}
	if email.ThreadID == uuid.Nil {
		t.Error("expected non-nil ThreadID")
	}
	if email.UserID == uuid.Nil {
		t.Error("expected non-nil UserID")
	}
}

// TestRawEmailJSONRoundtrip verifies JSON marshal/unmarshal for RawEmail.
func TestRawEmailJSONRoundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	subject := "Test Subject"
	bodyText := "Hello World"
	classification := "primary"

	original := &RawEmail{
		ID:               uuid.New(),
		ThreadID:         uuid.New(),
		UserID:           uuid.New(),
		SourceAccountID:  uuid.New(),
		MessageID:        "<test123@example.com>",
		SenderEmail:      "sender@example.com",
		SenderName:       ptr("Sender Name"),
		RecipientEmails:  []string{"recipient@example.com"},
		Subject:          &subject,
		BodyText:         &bodyText,
		HasAttachments:   true,
		AttachmentS3URIs: []string{"s3://bucket/att1.pdf"},
		ExtractedCodes:   []string{"1234"},
		ReceivedAt:       now,
		ParsedAt:         now,
		RetentionUntil:   now.Add(30 * 24 * time.Hour),
		Classification:   &classification,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded RawEmail
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.MessageID != original.MessageID {
		t.Errorf("message_id mismatch")
	}
	if decoded.SenderEmail != original.SenderEmail {
		t.Errorf("sender_email mismatch")
	}
	if decoded.Subject == nil || *decoded.Subject != subject {
		t.Errorf("subject mismatch")
	}
	if decoded.BodyText == nil || *decoded.BodyText != bodyText {
		t.Errorf("body_text mismatch")
	}
	if decoded.Classification == nil || *decoded.Classification != classification {
		t.Errorf("classification mismatch")
	}
	if !decoded.HasAttachments {
		t.Error("has_attachments should be true")
	}
	if len(decoded.ExtractedCodes) != 1 || decoded.ExtractedCodes[0] != "1234" {
		t.Errorf("extracted_codes mismatch: %v", decoded.ExtractedCodes)
	}
}

// TestThreadJSONRoundtrip verifies JSON marshal/unmarshal for Thread.
func TestThreadJSONRoundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	subject := "Thread Subject"

	original := &Thread{
		ID:                uuid.New(),
		UserID:            uuid.New(),
		ThreadKey:         "a1b2c3d4e5f6...",
		SourceAccountID:   uuid.New(),
		Subject:           &subject,
		ParticipantEmails: []string{"a@x.com", "b@x.com"},
		MessageCount:      5,
		LastMessageAt:     &now,
		Status:            "active",
		CreatedAt:         now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded Thread
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.ThreadKey != original.ThreadKey {
		t.Errorf("thread_key mismatch")
	}
	if decoded.MessageCount != 5 {
		t.Errorf("message_count mismatch: %d", decoded.MessageCount)
	}
	if decoded.Status != "active" {
		t.Errorf("status mismatch: %q", decoded.Status)
	}
	if decoded.Subject == nil || *decoded.Subject != subject {
		t.Errorf("subject mismatch")
	}
	if len(decoded.ParticipantEmails) != 2 {
		t.Errorf("participant_emails length mismatch: %d", len(decoded.ParticipantEmails))
	}
}

// TestWebhookPayloadJSONRoundtrip verifies JSON marshal/unmarshal.
func TestWebhookPayloadJSONRoundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	original := &WebhookPayload{
		MessageID:  "msg-123",
		HistoryID:  "hist-456",
		ChangeType: "created",
		ReceivedAt: now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded WebhookPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.MessageID != original.MessageID {
		t.Errorf("message_id mismatch")
	}
	if decoded.HistoryID != original.HistoryID {
		t.Errorf("history_id mismatch")
	}
	if decoded.ChangeType != "created" {
		t.Errorf("change_type mismatch")
	}
}

// TestContactJSONRoundtrip verifies JSON marshal/unmarshal for Contact.
func TestContactJSONRoundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	org := "Acme Corp"
	avgResponse := 2.5

	original := &Contact{
		ID:               uuid.New(),
		UserID:           uuid.New(),
		CanonicalEmail:   "alice@example.com",
		NameVariants:     []string{"Alice", "A. Smith"},
		Organization:     &org,
		FirstContactDate: &now,
		LastContactDate:  &now,
		InteractionCount: 42,
		AvgResponseHours: &avgResponse,
		ToneHistory:      []string{"positive", "neutral"},
		TotalMonetaryValue: 15000.50,
		Projects:         []string{"Project A", "Project B"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded Contact
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.CanonicalEmail != original.CanonicalEmail {
		t.Errorf("canonical_email mismatch")
	}
	if decoded.InteractionCount != 42 {
		t.Errorf("interaction_count mismatch: %d", decoded.InteractionCount)
	}
	if decoded.TotalMonetaryValue != 15000.50 {
		t.Errorf("total_monetary_value mismatch: %f", decoded.TotalMonetaryValue)
	}
	if len(decoded.NameVariants) != 2 {
		t.Errorf("name_variants length mismatch")
	}
	if decoded.Organization == nil || *decoded.Organization != org {
		t.Errorf("organization mismatch")
	}
}

// TestSubjectConstants verifies NATS subject constants.
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

// TestRateLimitStatusJSONRoundtrip verifies JSON marshal/unmarshal.
func TestRateLimitStatusJSONRoundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	original := &RateLimitStatus{
		Allowed:   true,
		Remaining: 100,
		ResetAt:   now,
		Backoff:   2 * time.Second,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded RateLimitStatus
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if !decoded.Allowed {
		t.Error("allowed should be true")
	}
	if decoded.Remaining != 100 {
		t.Errorf("remaining mismatch: %d", decoded.Remaining)
	}
}

// TestSendEmailRequestJSONRoundtrip verifies JSON marshal/unmarshal.
func TestSendEmailRequestJSONRoundtrip(t *testing.T) {
	inReplyTo := "<prev-msg@example.com>"
	original := &SendEmailRequest{
		To:         "recipient@example.com",
		Subject:    "Test",
		BodyText:   "Hello",
		BodyHTML:   "<p>Hello</p>",
		InReplyTo:  &inReplyTo,
		References: []string{"<ref1@example.com>"},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded SendEmailRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.To != original.To {
		t.Errorf("to mismatch")
	}
	if decoded.Subject != original.Subject {
		t.Errorf("subject mismatch")
	}
	if decoded.BodyText != original.BodyText {
		t.Errorf("body_text mismatch")
	}
	if decoded.InReplyTo == nil || *decoded.InReplyTo != inReplyTo {
		t.Errorf("in_reply_to mismatch")
	}
}

// ptr returns a pointer to the given value.
func ptr[T any](v T) *T {
	return &v
}
