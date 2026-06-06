// Package oauth tests the Google provider email sending functionality.
package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"

	"github.com/decisionstack/ingestion/internal/config"
	"github.com/decisionstack/ingestion/internal/models"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func strPtr(s string) *string {
	return &s
}

// newTestGoogleProviderWithServer creates a googleProvider that talks to the given test server.
func newTestGoogleProviderWithServer(serverURL string) *googleProvider {
	// We create a provider with a custom httpClient that redirects all requests
	// to our test server by rewriting URLs.
	p := newGoogleProvider(&config.Config{
		GoogleClientID:     "test-client-id",
		GoogleClientSecret: "test-client-secret",
		GoogleRedirectURI:  "http://localhost:8080/auth/google/callback",
	})

	// Replace the HTTP client with one that intercepts requests to Google APIs
	// and redirects them to our test server.
	p.httpClient = &http.Client{
		Transport: &testRoundTripper{serverURL: serverURL},
	}

	return p
}

// testRoundTripper intercepts requests to Gmail API and redirects to test server.
type testRoundTripper struct {
	serverURL string
}

func (t *testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rewrite Google API URLs to point to our test server
	if strings.Contains(req.URL.Host, "googleapis.com") {
		// Parse the original path to preserve it
		originalPath := req.URL.Path
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(t.serverURL, "http://")
		// Preserve the original path so the test server can route correctly
		req.URL.Path = originalPath
		req.Host = req.URL.Host
	}
	return http.DefaultTransport.RoundTrip(req)
}

// gmailSentRecord captures what was sent to the Gmail API.
type gmailSentRecord struct {
	Raw         string            `json:"raw"`
	ThreadID    string            `json:"threadId,omitempty"`
	LabelIDs    []string          `json:"labelIds,omitempty"`
	Headers     map[string]string // decoded from raw
	BodyText    string
	BodyHTML    string
	Subject     string
	To          string
	InReplyTo   string
	References  string
	ContentType string
}

// decodeGmailRaw decodes the base64url-encoded raw message and extracts headers.
func decodeGmailRaw(t *testing.T, raw string) *gmailSentRecord {
	t.Helper()

	data, err := base64.URLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("failed to decode base64url raw message: %v", err)
	}

	record := &gmailSentRecord{
		Raw:     raw,
		Headers: make(map[string]string),
	}

	// Parse the RFC 2822 message
	msg, err := mail.ReadMessage(strings.NewReader(string(data)))
	if err != nil {
		// Fallback: manual parsing if mail.ReadMessage fails on multipart
		record.parseManually(string(data))
		return record
	}

	record.Subject = msg.Header.Get("Subject")
	record.To = msg.Header.Get("To")
	record.InReplyTo = msg.Header.Get("In-Reply-To")
	record.References = msg.Header.Get("References")
	record.ContentType = msg.Header.Get("Content-Type")

	// Read body
	body, _ := io.ReadAll(msg.Body)
	record.BodyText = string(body)

	return record
}

// parseManually does a best-effort manual parse of the RFC 2822 message.
func (r *gmailSentRecord) parseManually(data string) {
	lines := strings.Split(data, "\r\n")
	inBody := false
	var bodyParts []string
	boundary := ""

	for _, line := range lines {
		if line == "" {
			inBody = true
			continue
		}
		if !inBody {
			if strings.HasPrefix(line, "Subject: ") {
				r.Subject = strings.TrimPrefix(line, "Subject: ")
			}
			if strings.HasPrefix(line, "To: ") {
				r.To = strings.TrimPrefix(line, "To: ")
			}
			if strings.HasPrefix(line, "In-Reply-To: ") {
				r.InReplyTo = strings.TrimPrefix(line, "In-Reply-To: ")
			}
			if strings.HasPrefix(line, "References: ") {
				r.References = strings.TrimPrefix(line, "References: ")
			}
			if strings.HasPrefix(line, "Content-Type: ") {
				r.ContentType = strings.TrimPrefix(line, "Content-Type: ")
				// Extract boundary
				if idx := strings.Index(line, `boundary="`); idx != -1 {
					start := idx + len(`boundary="`)
					end := strings.Index(line[start:], `"`)
					if end != -1 {
						boundary = line[start : start+end]
					}
				}
			}
		} else {
			bodyParts = append(bodyParts, line)
		}
	}

	body := strings.Join(bodyParts, "\n")

	// If multipart, extract text part
	if boundary != "" {
		parts := strings.Split(body, "--"+boundary)
		for _, part := range parts {
			if strings.Contains(part, "text/plain") {
				// Extract content after empty line
				if idx := strings.Index(part, "\n\n"); idx != -1 {
					r.BodyText = strings.TrimSpace(part[idx+2:])
					break
				}
				if idx := strings.Index(part, "\r\n\r\n"); idx != -1 {
					r.BodyText = strings.TrimSpace(part[idx+4:])
					break
				}
			}
		}
	} else {
		r.BodyText = body
	}
}

// ---------------------------------------------------------------------------
// Test: GoogleProvider.SendEmail
// ---------------------------------------------------------------------------

// TestGoogleProvider_SendEmail verifies email sending via the Gmail API.
func TestGoogleProvider_SendEmail(t *testing.T) {
	tests := []struct {
		name        string
		req         models.SendEmailRequest
		wantErr     bool
		apiStatus   int
		apiResponse string
	}{
		{
			name: "plain text email",
			req: models.SendEmailRequest{
				To:       "recipient@example.com",
				Subject:  "Test Subject",
				BodyText: "Hello, this is a test email.",
			},
			wantErr:   false,
			apiStatus: http.StatusOK,
			apiResponse: `{
				"id": "test-message-id-123",
				"threadId": "test-thread-id-456",
				"labelIds": ["SENT"]
			}`,
		},
		{
			name: "html email",
			req: models.SendEmailRequest{
				To:       "recipient@example.com",
				Subject:  "HTML Test",
				BodyText: "Plain text fallback",
				BodyHTML: "<p>Hello, this is <b>HTML</b>.</p>",
			},
			wantErr:   false,
			apiStatus: http.StatusOK,
			apiResponse: `{
				"id": "test-msg-html-789",
				"threadId": "test-thread-html-012"
			}`,
		},
		{
			name: "email with threading headers",
			req: models.SendEmailRequest{
				To:         "recipient@example.com",
				Subject:    "Re: Thread",
				BodyText:   "Reply body",
				InReplyTo:  strPtr("<original-msg-id@example.com>"),
				References: []string{"<msg1@example.com>", "<msg2@example.com>"},
			},
			wantErr:   false,
			apiStatus: http.StatusOK,
			apiResponse: `{
				"id": "test-reply-id-345",
				"threadId": "test-thread-reply-678"
			}`,
		},
		{
			name: "api returns error",
			req: models.SendEmailRequest{
				To:       "recipient@example.com",
				Subject:  "Error Test",
				BodyText: "This will fail.",
			},
			wantErr:     true,
			apiStatus:   http.StatusForbidden,
			apiResponse: `{"error": {"code": 403, "message": "Insufficient Permission"}}`,
		},
		{
			name: "empty access token",
			req: models.SendEmailRequest{
				To:       "recipient@example.com",
				Subject:  "Test",
				BodyText: "Body",
			},
			wantErr: true,
		},
		{
			name: "missing recipient",
			req: models.SendEmailRequest{
				To:       "",
				Subject:  "Test",
				BodyText: "Body",
			},
			wantErr: true,
		},
		{
			name: "missing subject",
			req: models.SendEmailRequest{
				To:       "recipient@example.com",
				Subject:  "",
				BodyText: "Body",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip tests that don't need API mocking
			if tt.name == "empty access token" {
				p := newGoogleProvider(&config.Config{
					GoogleClientID:     "test-id",
					GoogleClientSecret: "test-secret",
					GoogleRedirectURI:  "http://localhost/callback",
				})
				msgID, err := p.SendEmail(context.Background(), "", tt.req)
				if err == nil {
					t.Fatal("expected error for empty access token, got nil")
				}
				if msgID != "" {
					t.Errorf("expected empty message ID for error case, got %q", msgID)
				}
				return
			}

			if tt.name == "missing recipient" || tt.name == "missing subject" {
				p := newGoogleProvider(&config.Config{
					GoogleClientID:     "test-id",
					GoogleClientSecret: "test-secret",
					GoogleRedirectURI:  "http://localhost/callback",
				})
				msgID, err := p.SendEmail(context.Background(), "valid-token", tt.req)
				if err == nil {
					t.Fatal("expected error for missing fields, got nil")
				}
				if msgID != "" {
					t.Errorf("expected empty message ID for error case, got %q", msgID)
				}
				return
			}

			// Create test server that mimics Gmail API
			var capturedRaw string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify request path
				if !strings.Contains(r.URL.Path, "/gmail/v1/users/me/messages/send") {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}

				// Verify authorization header
				auth := r.Header.Get("Authorization")
				if auth == "" {
					t.Error("missing Authorization header")
				}

				// Read request body
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("failed to read request body: %v", err)
				}

				// Parse the Gmail API request to capture raw
				var gmailReq struct {
					Raw      string   `json:"raw"`
					ThreadID string   `json:"threadId,omitempty"`
					LabelIDs []string `json:"labelIds,omitempty"`
				}
				if err := json.Unmarshal(body, &gmailReq); err != nil {
					t.Fatalf("failed to unmarshal gmail request: %v", err)
				}
				capturedRaw = gmailReq.Raw

				// Return response
				w.WriteHeader(tt.apiStatus)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(tt.apiResponse))
			}))
			defer server.Close()

			p := newTestGoogleProviderWithServer(server.URL)

			msgID, err := p.SendEmail(context.Background(), "test-access-token", tt.req)

			if (err != nil) != tt.wantErr {
				t.Errorf("SendEmail() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if msgID != "" {
					t.Errorf("expected empty message ID for error case, got %q", msgID)
				}
				return
			}

			// Verify message ID is returned for successful sends
			if msgID == "" {
				t.Error("expected non-empty message ID for successful send")
			}

			// Verify the captured raw message
			if capturedRaw == "" {
				t.Fatal("expected raw message to be captured, got empty string")
			}

			// Decode and verify the raw message
			decoded := decodeGmailRaw(t, capturedRaw)

			if decoded.Subject != tt.req.Subject {
				t.Errorf("subject = %q, want %q", decoded.Subject, tt.req.Subject)
			}

			if decoded.To != tt.req.To {
				t.Errorf("to = %q, want %q", decoded.To, tt.req.To)
			}

			// Verify threading headers
			if tt.req.InReplyTo != nil {
				if decoded.InReplyTo != *tt.req.InReplyTo {
					t.Errorf("in_reply_to = %q, want %q", decoded.InReplyTo, *tt.req.InReplyTo)
				}
			}

			if len(tt.req.References) > 0 {
				wantRefs := strings.Join(tt.req.References, " ")
				if decoded.References != wantRefs {
					t.Errorf("references = %q, want %q", decoded.References, wantRefs)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// MIME building tests
// ---------------------------------------------------------------------------

// TestGoogleProvider_SendEmailMIMEStructure verifies the MIME message structure.
func TestGoogleProvider_SendEmailMIMEStructure(t *testing.T) {
	var capturedRaw string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var gmailReq struct {
			Raw string `json:"raw"`
		}
		json.Unmarshal(body, &gmailReq)
		capturedRaw = gmailReq.Raw
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id": "test-id"}`))
	}))
	defer server.Close()

	p := newTestGoogleProviderWithServer(server.URL)

	req := models.SendEmailRequest{
		To:       "alice@example.com",
		Subject:  "MIME Test",
		BodyText: "Plain text body",
		BodyHTML: "<p>HTML body</p>",
	}

	msgID, err := p.SendEmail(context.Background(), "test-token", req)
	if err != nil {
		t.Fatalf("SendEmail() unexpected error: %v", err)
	}
	if msgID == "" {
		t.Error("expected non-empty message ID for successful send")
	}

	if capturedRaw == "" {
		t.Fatal("no raw message captured")
	}

	// Decode the raw message
	decodedData, err := base64.URLEncoding.DecodeString(capturedRaw)
	if err != nil {
		t.Fatalf("failed to decode base64url: %v", err)
	}

	decoded := string(decodedData)

	// Verify MIME structure for multipart/alternative
	if !strings.Contains(decoded, "Content-Type: multipart/alternative") {
		t.Error("expected multipart/alternative content type")
	}

	// Verify boundary exists
	if !strings.Contains(decoded, "boundary=") {
		t.Error("expected boundary parameter in content type")
	}

	// Verify both parts exist
	if !strings.Contains(decoded, "Content-Type: text/plain") {
		t.Error("expected text/plain part")
	}
	if !strings.Contains(decoded, "Content-Type: text/html") {
		t.Error("expected text/html part")
	}

	// Verify body content in both parts
	if !strings.Contains(decoded, "Plain text body") {
		t.Error("expected plain text body content")
	}
	if !strings.Contains(decoded, "<p>HTML body</p>") {
		t.Error("expected HTML body content")
	}

	// Verify MIME-Version header
	if !strings.Contains(decoded, "MIME-Version: 1.0") {
		t.Error("expected MIME-Version: 1.0")
	}
}

// TestGoogleProvider_SendEmailPlainTextOnly verifies plain-text-only emails.
func TestGoogleProvider_SendEmailPlainTextOnly(t *testing.T) {
	var capturedRaw string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var gmailReq struct {
			Raw string `json:"raw"`
		}
		json.Unmarshal(body, &gmailReq)
		capturedRaw = gmailReq.Raw
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id": "test-id"}`))
	}))
	defer server.Close()

	p := newTestGoogleProviderWithServer(server.URL)

	req := models.SendEmailRequest{
		To:       "bob@example.com",
		Subject:  "Plain Text Only",
		BodyText: "Just plain text.",
	}

	msgID, err := p.SendEmail(context.Background(), "test-token", req)
	if err != nil {
		t.Fatalf("SendEmail() unexpected error: %v", err)
	}
	if msgID == "" {
		t.Error("expected non-empty message ID for successful send")
	}

	decodedData, err := base64.URLEncoding.DecodeString(capturedRaw)
	if err != nil {
		t.Fatalf("failed to decode base64url: %v", err)
	}

	decoded := string(decodedData)

	// Should NOT be multipart for plain-text-only
	if strings.Contains(decoded, "multipart/alternative") {
		t.Error("plain-text email should not use multipart/alternative")
	}

	// Should have text/plain content type
	if !strings.Contains(decoded, "Content-Type: text/plain") {
		t.Error("expected text/plain content type")
	}

	// Should contain the body
	if !strings.Contains(decoded, "Just plain text.") {
		t.Error("expected body text")
	}
}

// ---------------------------------------------------------------------------
// Threading header tests
// ---------------------------------------------------------------------------

// TestGoogleProvider_SendEmailThreadingHeaders verifies threading headers are included.
func TestGoogleProvider_SendEmailThreadingHeaders(t *testing.T) {
	var capturedRaw string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var gmailReq struct {
			Raw string `json:"raw"`
		}
		json.Unmarshal(body, &gmailReq)
		capturedRaw = gmailReq.Raw
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id": "test-id"}`))
	}))
	defer server.Close()

	p := newTestGoogleProviderWithServer(server.URL)

	inReplyTo := "<abc123@example.com>"
	references := []string{"<msg1@example.com>", "<msg2@example.com>", "<msg3@example.com>"}

	req := models.SendEmailRequest{
		To:         "thread@example.com",
		Subject:    "Re: Discussion",
		BodyText:   "Replying to the thread.",
		InReplyTo:  &inReplyTo,
		References: references,
	}

	msgID, err := p.SendEmail(context.Background(), "test-token", req)
	if err != nil {
		t.Fatalf("SendEmail() unexpected error: %v", err)
	}
	if msgID == "" {
		t.Error("expected non-empty message ID for successful send")
	}

	decodedData, err := base64.URLEncoding.DecodeString(capturedRaw)
	if err != nil {
		t.Fatalf("failed to decode base64url: %v", err)
	}

	decoded := string(decodedData)

	// Verify In-Reply-To header
	if !strings.Contains(decoded, "In-Reply-To: <abc123@example.com>") {
		t.Errorf("expected In-Reply-To header, got:\n%s", decoded)
	}

	// Verify References header with all message IDs
	wantRefs := "References: <msg1@example.com> <msg2@example.com> <msg3@example.com>"
	if !strings.Contains(decoded, wantRefs) {
		t.Errorf("expected References header with all message IDs\nwant: %s\ngot:\n%s", wantRefs, decoded)
	}
}

// TestGoogleProvider_SendEmailNoThreadingHeaders verifies no headers when not provided.
func TestGoogleProvider_SendEmailNoThreadingHeaders(t *testing.T) {
	var capturedRaw string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var gmailReq struct {
			Raw string `json:"raw"`
		}
		json.Unmarshal(body, &gmailReq)
		capturedRaw = gmailReq.Raw
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id": "test-id"}`))
	}))
	defer server.Close()

	p := newTestGoogleProviderWithServer(server.URL)

	req := models.SendEmailRequest{
		To:       "simple@example.com",
		Subject:  "New Thread",
		BodyText: "Starting fresh.",
	}

	msgID, err := p.SendEmail(context.Background(), "test-token", req)
	if err != nil {
		t.Fatalf("SendEmail() unexpected error: %v", err)
	}
	if msgID == "" {
		t.Error("expected non-empty message ID for successful send")
	}

	decodedData, err := base64.URLEncoding.DecodeString(capturedRaw)
	if err != nil {
		t.Fatalf("failed to decode base64url: %v", err)
	}

	decoded := string(decodedData)

	// Should NOT have In-Reply-To
	if strings.Contains(decoded, "In-Reply-To:") {
		t.Error("expected no In-Reply-To header for new thread")
	}

	// Should NOT have References
	if strings.Contains(decoded, "References:") {
		t.Error("expected no References header for new thread")
	}
}

// ---------------------------------------------------------------------------
// Base64url encoding tests
// ---------------------------------------------------------------------------

// TestGoogleProvider_SendEmailBase64URLEncoding verifies base64url encoding is used.
func TestGoogleProvider_SendEmailBase64URLEncoding(t *testing.T) {
	var capturedRaw string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var gmailReq struct {
			Raw string `json:"raw"`
		}
		json.Unmarshal(body, &gmailReq)
		capturedRaw = gmailReq.Raw
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id": "test-id"}`))
	}))
	defer server.Close()

	p := newTestGoogleProviderWithServer(server.URL)

	req := models.SendEmailRequest{
		To:       "test@example.com",
		Subject:  "Encoding Test",
		BodyText: "Test body with special chars: <>\"&",
	}

	msgID, err := p.SendEmail(context.Background(), "test-token", req)
	if err != nil {
		t.Fatalf("SendEmail() unexpected error: %v", err)
	}
	if msgID == "" {
		t.Error("expected non-empty message ID for successful send")
	}

	// Verify base64url encoding (no standard base64 padding with =)
	if strings.HasSuffix(capturedRaw, "=") {
		t.Error("expected base64url encoding without padding, got padding")
	}

	// Verify it's valid base64url
	decoded, err := base64.URLEncoding.DecodeString(capturedRaw)
	if err != nil {
		t.Fatalf("failed to decode base64url: %v", err)
	}

	// Verify decoded content is a valid RFC 2822 message
	decodedStr := string(decoded)
	if !strings.Contains(decodedStr, "To: test@example.com") {
		t.Error("expected To header in decoded message")
	}
	if !strings.Contains(decodedStr, "Subject: Encoding Test") {
		t.Error("expected Subject header in decoded message")
	}
}

// TestGoogleProvider_SendEmailBase64URLEncodingNoPadding specifically verifies no padding.
func TestGoogleProvider_SendEmailBase64URLEncodingNoPadding(t *testing.T) {
	// Test with various body lengths to hit different padding scenarios
	bodies := []string{
		"A",                         // 1 byte -> needs padding in std base64
		"AB",                        // 2 bytes -> needs padding
		"ABC",                       // 3 bytes -> no padding
		"Hello",                     // 5 bytes -> needs padding
		"Hello World!",              // 12 bytes -> no padding
		"Special: <>\"&'",           // 15 bytes -> needs padding
		"Line1\r\nLine2\r\nLine3",  // 18 bytes -> no padding
	}

	for _, body := range bodies {
		t.Run(fmt.Sprintf("body_%db", len(body)), func(t *testing.T) {
			var capturedRaw string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				bodyBytes, _ := io.ReadAll(r.Body)
				var gmailReq struct {
					Raw string `json:"raw"`
				}
				json.Unmarshal(bodyBytes, &gmailReq)
				capturedRaw = gmailReq.Raw
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"id": "test-id"}`))
			}))
			defer server.Close()

			p := newTestGoogleProviderWithServer(server.URL)

			req := models.SendEmailRequest{
				To:       "test@example.com",
				Subject:  "Padding Test",
				BodyText: body,
			}

			msgID, err := p.SendEmail(context.Background(), "test-token", req)
			if err != nil {
				t.Fatalf("SendEmail() unexpected error: %v", err)
			}
			if msgID == "" {
				t.Error("expected non-empty message ID for successful send")
			}

			// Verify no padding characters
			if strings.Contains(capturedRaw, "=") {
				t.Errorf("base64url should not contain padding '=', got: %s", capturedRaw)
			}

			// Verify it can be decoded
			_, err = base64.URLEncoding.DecodeString(capturedRaw)
			if err != nil {
				t.Errorf("failed to decode base64url: %v, raw: %s", err, capturedRaw)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Error handling tests
// ---------------------------------------------------------------------------

// TestGoogleProvider_SendEmailNetworkError verifies handling of network failures.
func TestGoogleProvider_SendEmailNetworkError(t *testing.T) {
	// Create a server that immediately closes
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	p := newTestGoogleProviderWithServer(server.URL)

	req := models.SendEmailRequest{
		To:       "test@example.com",
		Subject:  "Network Error Test",
		BodyText: "Body",
	}

	msgID, err := p.SendEmail(context.Background(), "test-token", req)
	if err == nil {
		t.Fatal("expected error for network failure, got nil")
	}
	if msgID != "" {
		t.Errorf("expected empty message ID for error case, got %q", msgID)
	}
}

// TestGoogleProvider_SendEmailRateLimit verifies handling of 429 rate limit.
func TestGoogleProvider_SendEmailRateLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error": {"code": 429, "message": "Rate limit exceeded"}}`))
	}))
	defer server.Close()

	p := newTestGoogleProviderWithServer(server.URL)

	req := models.SendEmailRequest{
		To:       "test@example.com",
		Subject:  "Rate Limit Test",
		BodyText: "Body",
	}

	msgID, err := p.SendEmail(context.Background(), "test-token", req)
	if err == nil {
		t.Fatal("expected error for rate limit, got nil")
	}
	if msgID != "" {
		t.Errorf("expected empty message ID for error case, got %q", msgID)
	}
	if !strings.Contains(err.Error(), "429") && !strings.Contains(err.Error(), "failed to send") {
		t.Errorf("expected rate limit error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Compile-time interface checks
// ---------------------------------------------------------------------------

// TestGoogleProviderImplementsEmailProvider verifies the provider implements EmailProvider.
func TestGoogleProviderImplementsEmailProvider(t *testing.T) {
	var _ models.EmailProvider = (*googleProvider)(nil)
}

// TestGoogleProvider_SendEmailReturnsMessageID verifies that SendEmail returns
// the Gmail message ID from the API response.
func TestGoogleProvider_SendEmailReturnsMessageID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id": "real-msg-id-123", "threadId": "thread-456"}`))
	}))
	defer server.Close()

	p := newTestGoogleProviderWithServer(server.URL)

	req := models.SendEmailRequest{
		To:       "test@example.com",
		Subject:  "Message ID Test",
		BodyText: "Testing message ID return.",
	}

	msgID, err := p.SendEmail(context.Background(), "test-token", req)
	if err != nil {
		t.Fatalf("SendEmail() unexpected error: %v", err)
	}
	if msgID != "real-msg-id-123" {
		t.Errorf("message ID = %q, want %q", msgID, "real-msg-id-123")
	}
}

// TestGoogleProvider_SendEmailReturnsEmptyIDOnError verifies that SendEmail
// returns an empty message ID when the API call fails.
func TestGoogleProvider_SendEmailReturnsEmptyIDOnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error": {"code": 403, "message": "Forbidden"}}`))
	}))
	defer server.Close()

	p := newTestGoogleProviderWithServer(server.URL)

	req := models.SendEmailRequest{
		To:       "test@example.com",
		Subject:  "Error Test",
		BodyText: "Testing error case.",
	}

	msgID, err := p.SendEmail(context.Background(), "test-token", req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if msgID != "" {
		t.Errorf("expected empty message ID on error, got %q", msgID)
	}
}