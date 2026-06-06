// Package mocks provides test doubles for OAuth authentication components.
package mocks

import (
	"context"
	"fmt"
	"sync"
	"time"

	"ingestion/internal/models"
)

// MockProvider is a configurable test double that implements models.OAuthProvider.
// Use it in unit tests for webhook handlers, polling workers, and other
// components that depend on OAuthProvider without making real API calls.
//
// All methods are safe for concurrent use.
type MockProvider struct {
	mu sync.RWMutex

	// NameReturn is the value returned by Name().
	NameReturn string

	// AuthURLReturn is the value returned by AuthURL().
	AuthURLReturn string

	// ExchangeReturn is the value returned by Exchange().
	ExchangeReturn *models.TokenPair
	// ExchangeErr is the error returned by Exchange().
	ExchangeErr error

	// RefreshReturn is the value returned by Refresh().
	RefreshReturn *models.TokenPair
	// RefreshErr is the error returned by Refresh().
	RefreshErr error

	// RevokeErr is the error returned by Revoke().
	RevokeErr error

	// ValidateWebhookReturn is the value returned by ValidateWebhook().
	ValidateWebhookReturn *models.WebhookPayload
	// ValidateWebhookErr is the error returned by ValidateWebhook().
	ValidateWebhookErr error

	// FetchSentHistoryReturn is the value returned by FetchSentHistory().
	FetchSentHistoryReturn []models.ParsedEmail
	// FetchSentHistoryErr is the error returned by FetchSentHistory().
	FetchSentHistoryErr error

	// SendEmailErr is the error returned by SendEmail().
	SendEmailErr error

	// Call tracking for test assertions
	AuthURLCalls       []AuthURLCall
	ExchangeCalls      []ExchangeCall
	RefreshCalls       []RefreshCall
	RevokeCalls        []RevokeCall
	ValidateWebhookCalls []ValidateWebhookCall
	FetchSentHistoryCalls []FetchSentHistoryCall
	SendEmailCalls     []SendEmailCall
}

// AuthURLCall records a call to AuthURL.
type AuthURLCall struct {
	State       string
	RedirectURI string
}

// ExchangeCall records a call to Exchange.
type ExchangeCall struct {
	Code        string
	RedirectURI string
}

// RefreshCall records a call to Refresh.
type RefreshCall struct {
	RefreshToken string
}

// RevokeCall records a call to Revoke.
type RevokeCall struct {
	Token string
}

// ValidateWebhookCall records a call to ValidateWebhook.
type ValidateWebhookCall struct {
	Payload []byte
	Headers map[string]string
}

// FetchSentHistoryCall records a call to FetchSentHistory.
type FetchSentHistoryCall struct {
	AccessToken string
	DaysBack    int
}

// SendEmailCall records a call to SendEmail.
type SendEmailCall struct {
	AccessToken string
	Request     models.SendEmailRequest
}

// ---------------------------------------------------------------------------
// Factory helpers
// ---------------------------------------------------------------------------

// NewMockGmailProvider returns a MockProvider configured for Gmail.
func NewMockGmailProvider() *MockProvider {
	return &MockProvider{
		NameReturn:    "gmail",
		AuthURLReturn: "https://accounts.google.com/o/oauth2/v2/auth?mock=true",
	}
}

// NewMockOutlookProvider returns a MockProvider configured for Outlook.
func NewMockOutlookProvider() *MockProvider {
	return &MockProvider{
		NameReturn:    "outlook",
		AuthURLReturn: "https://login.microsoftonline.com/common/oauth2/v2.0/authorize?mock=true",
	}
}

// ---------------------------------------------------------------------------
// OAuthProvider implementation
// ---------------------------------------------------------------------------

// Name returns the configured provider name.
func (m *MockProvider) Name() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.NameReturn
}

// AuthURL returns the configured authorization URL and records the call.
func (m *MockProvider) AuthURL(state string, redirectURI string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.AuthURLCalls = append(m.AuthURLCalls, AuthURLCall{
		State:       state,
		RedirectURI: redirectURI,
	})
	return m.AuthURLReturn
}

// Exchange returns the configured result and records the call.
func (m *MockProvider) Exchange(_ context.Context, code string, redirectURI string) (*models.TokenPair, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ExchangeCalls = append(m.ExchangeCalls, ExchangeCall{
		Code:        code,
		RedirectURI: redirectURI,
	})
	if m.ExchangeErr != nil {
		return nil, m.ExchangeErr
	}
	return m.ExchangeReturn, nil
}

// Refresh returns the configured result and records the call.
func (m *MockProvider) Refresh(_ context.Context, refreshToken string) (*models.TokenPair, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RefreshCalls = append(m.RefreshCalls, RefreshCall{
		RefreshToken: refreshToken,
	})
	if m.RefreshErr != nil {
		return nil, m.RefreshErr
	}
	return m.RefreshReturn, nil
}

// Revoke returns the configured error and records the call.
func (m *MockProvider) Revoke(_ context.Context, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RevokeCalls = append(m.RevokeCalls, RevokeCall{
		Token: token,
	})
	return m.RevokeErr
}

// ValidateWebhook returns the configured result and records the call.
func (m *MockProvider) ValidateWebhook(payload []byte, headers map[string]string) (*models.WebhookPayload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Copy payload to avoid mutation issues in tests
	payloadCopy := make([]byte, len(payload))
	copy(payloadCopy, payload)

	// Copy headers
	headersCopy := make(map[string]string, len(headers))
	for k, v := range headers {
		headersCopy[k] = v
	}

	m.ValidateWebhookCalls = append(m.ValidateWebhookCalls, ValidateWebhookCall{
		Payload: payloadCopy,
		Headers: headersCopy,
	})
	if m.ValidateWebhookErr != nil {
		return nil, m.ValidateWebhookErr
	}
	return m.ValidateWebhookReturn, nil
}

// FetchSentHistory returns the configured result and records the call.
func (m *MockProvider) FetchSentHistory(_ context.Context, accessToken string, daysBack int) ([]models.ParsedEmail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.FetchSentHistoryCalls = append(m.FetchSentHistoryCalls, FetchSentHistoryCall{
		AccessToken: accessToken,
		DaysBack:    daysBack,
	})
	if m.FetchSentHistoryErr != nil {
		return nil, m.FetchSentHistoryErr
	}
	return m.FetchSentHistoryReturn, nil
}

// SendEmail returns the configured error and records the call.
func (m *MockProvider) SendEmail(_ context.Context, accessToken string, req models.SendEmailRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SendEmailCalls = append(m.SendEmailCalls, SendEmailCall{
		AccessToken: accessToken,
		Request:     req,
	})
	return m.SendEmailErr
}

// ---------------------------------------------------------------------------
// Test assertion helpers
// ---------------------------------------------------------------------------

// AuthURLCalled returns the number of AuthURL calls.
func (m *MockProvider) AuthURLCalled() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.AuthURLCalls)
}

// ExchangeCalled returns the number of Exchange calls.
func (m *MockProvider) ExchangeCalled() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.ExchangeCalls)
}

// RefreshCalled returns the number of Refresh calls.
func (m *MockProvider) RefreshCalled() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.RefreshCalls)
}

// RevokeCalled returns the number of Revoke calls.
func (m *MockProvider) RevokeCalled() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.RevokeCalls)
}

// ValidateWebhookCalled returns the number of ValidateWebhook calls.
func (m *MockProvider) ValidateWebhookCalled() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.ValidateWebhookCalls)
}

// FetchSentHistoryCalled returns the number of FetchSentHistory calls.
func (m *MockProvider) FetchSentHistoryCalled() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.FetchSentHistoryCalls)
}

// SendEmailCalled returns the number of SendEmail calls.
func (m *MockProvider) SendEmailCalled() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.SendEmailCalls)
}

// Reset clears all call tracking and configured returns/errors.
func (m *MockProvider) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.NameReturn = ""
	m.AuthURLReturn = ""
	m.ExchangeReturn = nil
	m.ExchangeErr = nil
	m.RefreshReturn = nil
	m.RefreshErr = nil
	m.RevokeErr = nil
	m.ValidateWebhookReturn = nil
	m.ValidateWebhookErr = nil
	m.FetchSentHistoryReturn = nil
	m.FetchSentHistoryErr = nil
	m.SendEmailErr = nil

	m.AuthURLCalls = nil
	m.ExchangeCalls = nil
	m.RefreshCalls = nil
	m.RevokeCalls = nil
	m.ValidateWebhookCalls = nil
	m.FetchSentHistoryCalls = nil
	m.SendEmailCalls = nil
}

// ---------------------------------------------------------------------------
// Pre-built test responses
// ---------------------------------------------------------------------------

// DefaultTokenPair returns a valid TokenPair for use in tests.
func DefaultTokenPair() *models.TokenPair {
	now := time.Now().UTC()
	exp := now.Add(15 * time.Minute)
	accessToken := "test-access-token"
	refreshToken := "test-refresh-token"

	return &models.TokenPair{
		RefreshToken: &models.EncryptedToken{
			Ciphertext: []byte(refreshToken),
			Nonce:      make([]byte, 12),
			KeyID:      "test-key",
		},
		AccessToken: &models.EncryptedToken{
			Ciphertext: []byte(accessToken),
			Nonce:      make([]byte, 12),
			KeyID:      "test-key",
		},
		AccessTokenPlaintext: &accessToken,
		ExpiresAt:            &exp,
		ScopeGranted:         []string{"email", "profile"},
	}
}

// ExpiredTokenError returns an IngestionError simulating invalid_grant.
func ExpiredTokenError() error {
	return models.IngestionError{
		Code:    models.ErrCodeOAuthExpired,
		Message: "The refresh token has expired or been revoked",
		Retry:   false,
	}
}

// DefaultWebhookPayload returns a valid WebhookPayload for use in tests.
func DefaultWebhookPayload() *models.WebhookPayload {
	return &models.WebhookPayload{
		MessageID:  "test-message-id",
		HistoryID:  "12345",
		ChangeType: "created",
		ReceivedAt: time.Now().UTC(),
	}
}

// DefaultParsedEmails returns sample parsed emails for use in tests.
func DefaultParsedEmails() []models.ParsedEmail {
	return []models.ParsedEmail{
		{
			MessageID:   "msg-1",
			Source:      "gmail",
			SenderEmail: "sender@example.com",
			Subject:     "Test Email 1",
			BodyText:    "Hello, this is a test email.",
			ReceivedAt:  time.Now().UTC().Add(-1 * time.Hour),
		},
		{
			MessageID:   "msg-2",
			Source:      "gmail",
			SenderEmail: "other@example.com",
			Subject:     "Test Email 2",
			BodyText:    "Another test email body.",
			ReceivedAt:  time.Now().UTC().Add(-2 * time.Hour),
		},
	}
}

// Ensure MockProvider implements OAuthProvider at compile time.
var _ models.OAuthProvider = (*MockProvider)(nil)

// ErrNotImplemented can be used as a default error for unconfigured mock methods.
var ErrNotImplemented = fmt.Errorf("mock method not configured")
