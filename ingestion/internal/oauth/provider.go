// Package oauth provides OAuth 2.0 and MSAL authentication implementations
// for Gmail and Outlook, with secure token encryption via KMS.
package oauth

import (
	"fmt"
	"net/http"
	"time"

	"github.com/decisionstack/ingestion/internal/config"
	"github.com/decisionstack/ingestion/internal/models"
)

// ProviderName identifies the supported OAuth providers.
type ProviderName string

const (
	// ProviderGmail is the Google Gmail OAuth provider.
	ProviderGmail ProviderName = "gmail"
	// ProviderOutlook is the Microsoft Outlook MSAL provider.
	ProviderOutlook ProviderName = "outlook"
)

// baseProvider holds common HTTP client configuration shared by all providers.
type baseProvider struct {
	httpClient *http.Client
}

// newBaseProvider creates a baseProvider with sensible defaults.
func newBaseProvider() baseProvider {
	return baseProvider{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewProvider creates the appropriate OAuthProvider implementation based on the
// provider name. Returns an error if the provider is not supported.
//
// Supported providers:
//   - "gmail": Google OAuth 2.0 for Gmail
//   - "outlook": Microsoft MSAL for Outlook
func NewProvider(name ProviderName, cfg *config.Config) (models.OAuthProvider, error) {
	switch name {
	case ProviderGmail:
		return newGoogleProvider(cfg), nil
	case ProviderOutlook:
		return newMicrosoftProvider(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported OAuth provider: %q (expected %q or %q)",
			name, ProviderGmail, ProviderOutlook)
	}
}

// ProviderNames returns all supported provider names.
func ProviderNames() []ProviderName {
	return []ProviderName{ProviderGmail, ProviderOutlook}
}

// IsValidProvider checks if the given provider name is supported.
func IsValidProvider(name string) bool {
	switch ProviderName(name) {
	case ProviderGmail, ProviderOutlook:
		return true
	default:
		return false
	}
}
