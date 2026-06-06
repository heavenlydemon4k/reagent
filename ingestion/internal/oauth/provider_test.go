// Package oauth tests the OAuth provider factory.
package oauth

import (
	"testing"

	"github.com/decisionstack/ingestion/internal/config"
)

// TestNewProviderGmail verifies that ProviderGmail returns a googleProvider.
func TestNewProviderGmail(t *testing.T) {
	cfg := &config.Config{
		GoogleClientID:     "test-client-id",
		GoogleClientSecret: "test-client-secret",
		GoogleRedirectURI:  "http://localhost:8080/auth/google/callback",
	}

	provider, err := NewProvider(ProviderGmail, cfg)
	if err != nil {
		t.Fatalf("NewProvider(gmail) failed: %v", err)
	}
	if provider == nil {
		t.Fatal("NewProvider(gmail) returned nil")
	}
	if provider.Name() != "gmail" {
		t.Errorf("provider.Name() = %q, want %q", provider.Name(), "gmail")
	}
}

// TestNewProviderOutlook verifies that ProviderOutlook returns a microsoftProvider.
func TestNewProviderOutlook(t *testing.T) {
	cfg := &config.Config{
		MicrosoftClientID:     "test-ms-client-id",
		MicrosoftClientSecret: "test-ms-client-secret",
		MicrosoftRedirectURI:  "http://localhost:8080/auth/microsoft/callback",
	}

	provider, err := NewProvider(ProviderOutlook, cfg)
	if err != nil {
		t.Fatalf("NewProvider(outlook) failed: %v", err)
	}
	if provider == nil {
		t.Fatal("NewProvider(outlook) returned nil")
	}
	if provider.Name() != "outlook" {
		t.Errorf("provider.Name() = %q, want %q", provider.Name(), "outlook")
	}
}

// TestNewProviderUnsupported verifies error for unsupported provider.
func TestNewProviderUnsupported(t *testing.T) {
	cfg := &config.Config{}

	_, err := NewProvider("yahoo", cfg)
	if err == nil {
		t.Error("expected error for unsupported provider")
	}

	_, err = NewProvider("", cfg)
	if err == nil {
		t.Error("expected error for empty provider name")
	}

	_, err = NewProvider("exchange", cfg)
	if err == nil {
		t.Error("expected error for unsupported provider 'exchange'")
	}
}

// TestProviderNames verifies ProviderNames returns all supported providers.
func TestProviderNames(t *testing.T) {
	names := ProviderNames()

	if len(names) != 2 {
		t.Errorf("expected 2 provider names, got %d: %v", len(names), names)
	}

	// Check both providers are present
	hasGmail := false
	hasOutlook := false
	for _, n := range names {
		switch n {
		case ProviderGmail:
			hasGmail = true
		case ProviderOutlook:
			hasOutlook = true
		}
	}

	if !hasGmail {
		t.Error("ProviderNames missing gmail")
	}
	if !hasOutlook {
		t.Error("ProviderNames missing outlook")
	}
}

// TestIsValidProvider validates known-good and known-bad provider names.
func TestIsValidProvider(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"gmail", true},
		{"outlook", true},
		{"GMAIL", false},    // case-sensitive
		{"OUTLOOK", false},  // case-sensitive
		{"yahoo", false},
		{"", false},
		{"exchange", false},
		{"google", false},
		{"microsoft", false},
		{"gmail ", false},  // trailing space
		{" gmail", false},  // leading space
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidProvider(tt.name)
			if got != tt.expected {
				t.Errorf("IsValidProvider(%q) = %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}

// TestProviderNameConstants verifies provider name constants.
func TestProviderNameConstants(t *testing.T) {
	if ProviderGmail != "gmail" {
		t.Errorf("ProviderGmail = %q, want %q", ProviderGmail, "gmail")
	}
	if ProviderOutlook != "outlook" {
		t.Errorf("ProviderOutlook = %q, want %q", ProviderOutlook, "outlook")
	}
}

// TestNewProviderGmailAuthURL verifies that the Gmail provider generates a valid auth URL.
func TestNewProviderGmailAuthURL(t *testing.T) {
	cfg := &config.Config{
		GoogleClientID:     "test-client-id",
		GoogleClientSecret: "test-client-secret",
		GoogleRedirectURI:  "http://localhost:8080/auth/google/callback",
	}

	provider, err := NewProvider(ProviderGmail, cfg)
	if err != nil {
		t.Fatalf("NewProvider(gmail) failed: %v", err)
	}

	state := "test-state-123"
	authURL := provider.AuthURL(state, "")

	if authURL == "" {
		t.Error("AuthURL returned empty string")
	}
	if authURL[:4] != "http" {
		t.Errorf("AuthURL should start with http, got: %s", authURL)
	}
}

// TestBaseProviderTimeout verifies the base HTTP client timeout.
func TestBaseProviderTimeout(t *testing.T) {
	bp := newBaseProvider()
	if bp.httpClient == nil {
		t.Fatal("httpClient should not be nil")
	}
	if bp.httpClient.Timeout == 0 {
		t.Error("httpClient.Timeout should be set")
	}
}
