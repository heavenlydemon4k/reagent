// Package oauth provides PostgreSQL-backed secure token storage.
package oauth

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/decisionstack/ingestion/internal/crypto"
	"github.com/decisionstack/ingestion/internal/models"
)

// TokenStore handles persistence of encrypted OAuth tokens in PostgreSQL.
// All token fields are encrypted at rest using AES-256-GCM with KMS-managed DEKs.
type TokenStore struct {
	db        *sql.DB
	crypto    *crypto.TokenCrypto
	providers map[string]models.OAuthProvider // provider name -> OAuthProvider
}

// NewTokenStore creates a new TokenStore.
func NewTokenStore(db *sql.DB, crypto *crypto.TokenCrypto) *TokenStore {
	return &TokenStore{
		db:     db,
		crypto: crypto,
	}
}

// SaveTokens persists a new TokenPair for the given account ID.
// Both refresh and access tokens are encrypted before storage.
// This method should be used for the initial token save after OAuth exchange.
func (s *TokenStore) SaveTokens(ctx context.Context, accountID uuid.UUID, pair *models.TokenPair) error {
	if pair == nil {
		return fmt.Errorf("token pair is nil")
	}

	// Encrypt refresh token
	var refreshJSON []byte
	if pair.RefreshToken != nil {
		if len(pair.RefreshToken.Ciphertext) > 0 && !s.isEncrypted(pair.RefreshToken) {
			plaintext := string(pair.RefreshToken.Ciphertext)
			encRefresh, err := s.crypto.EncryptToken(ctx, plaintext, pair.RefreshToken.KeyID)
			if err != nil {
				return fmt.Errorf("failed to encrypt refresh token: %w", err)
			}
			pair.RefreshToken = encRefresh
		}
		var err error
		refreshJSON, err = json.Marshal(pair.RefreshToken)
		if err != nil {
			return fmt.Errorf("failed to marshal refresh token: %w", err)
		}
	}

	// Encrypt access token
	var accessJSON []byte
	if pair.AccessToken != nil {
		if len(pair.AccessToken.Ciphertext) > 0 && !s.isEncrypted(pair.AccessToken) {
			plaintext := string(pair.AccessToken.Ciphertext)
			encAccess, err := s.crypto.EncryptToken(ctx, plaintext, pair.AccessToken.KeyID)
			if err != nil {
				return fmt.Errorf("failed to encrypt access token: %w", err)
			}
			pair.AccessToken = encAccess
		}
		var err error
		accessJSON, err = json.Marshal(pair.AccessToken)
		if err != nil {
			return fmt.Errorf("failed to marshal access token: %w", err)
		}
	}

	// Build expires_at
	expiresAt := sql.NullTime{}
	if pair.ExpiresAt != nil {
		expiresAt = sql.NullTime{Time: *pair.ExpiresAt, Valid: true}
	}

	scopeStr := ""
	if len(pair.ScopeGranted) > 0 {
		scopeBytes, _ := json.Marshal(pair.ScopeGranted)
		scopeStr = string(scopeBytes)
	}

	// Insert or update the email_accounts row
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO email_accounts (
			id, refresh_token, access_token, expires_at, scope_granted, is_active, updated_at
		) VALUES ($1, $2, $3, $4, $5, true, NOW())
		ON CONFLICT (id) DO UPDATE SET
			refresh_token = EXCLUDED.refresh_token,
			access_token = EXCLUDED.access_token,
			expires_at = EXCLUDED.expires_at,
			scope_granted = EXCLUDED.scope_granted,
			is_active = true,
			updated_at = NOW()
	`, accountID, refreshJSON, accessJSON, expiresAt, scopeStr)

	if err != nil {
		return fmt.Errorf("failed to save tokens to database: %w", err)
	}

	return nil
}

// LoadTokens retrieves and decrypts the TokenPair for the given account.
// The access token is decrypted for in-memory use (15-min TTL).
// The refresh token remains encrypted in memory and is only decrypted when needed.
func (s *TokenStore) LoadTokens(ctx context.Context, accountID uuid.UUID) (*models.TokenPair, error) {
	var refreshJSON, accessJSON []byte
	var expiresAt sql.NullTime
	var scopeStr string
	var isActive bool

	err := s.db.QueryRowContext(ctx, `
		SELECT refresh_token, access_token, expires_at, scope_granted, is_active
		FROM email_accounts WHERE id = $1
	`, accountID).Scan(&refreshJSON, &accessJSON, &expiresAt, &scopeStr, &isActive)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("account %s not found", accountID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load tokens: %w", err)
	}

	if !isActive {
		return nil, &models.IngestionError{
			Code:    models.ErrCodeOAuthExpired,
			Message: fmt.Sprintf("account %s is deactivated", accountID),
			Retry:   false,
		}
	}

	pair := &models.TokenPair{}

	// Decrypt refresh token
	if len(refreshJSON) > 0 {
		var encRefresh models.EncryptedToken
		if err := json.Unmarshal(refreshJSON, &encRefresh); err != nil {
			return nil, fmt.Errorf("failed to unmarshal refresh token: %w", err)
		}
		pair.RefreshToken = &encRefresh

		// Only decrypt refresh token when explicitly needed
		// (caller will decrypt when calling Refresh)
	}

	// Decrypt access token for in-memory use
	if len(accessJSON) > 0 {
		var encAccess models.EncryptedToken
		if err := json.Unmarshal(accessJSON, &encAccess); err != nil {
			return nil, fmt.Errorf("failed to unmarshal access token: %w", err)
		}
		pair.AccessToken = &encAccess

		// Decrypt for in-memory plaintext (15-min TTL)
		plaintext, err := s.crypto.DecryptToken(ctx, &encAccess)
		if err != nil {
			return nil, &models.IngestionError{
				Code:    models.ErrCodeTokenDecryptFailed,
				Message: fmt.Sprintf("failed to decrypt access token: %v", err),
				Retry:   true,
			}
		}
		pair.AccessTokenPlaintext = &plaintext
	}

	if expiresAt.Valid {
		pair.ExpiresAt = &expiresAt.Time
	}

	if scopeStr != "" {
		var scopes []string
		if err := json.Unmarshal([]byte(scopeStr), &scopes); err == nil {
			pair.ScopeGranted = scopes
		}
	}

	return pair, nil
}

// UpdateAccessToken updates only the access token (used after refresh).
// The new access token is encrypted before storage.
// This also implements automatic rotation: if a new refresh token is provided,
// it is encrypted and stored as well.
func (s *TokenStore) UpdateAccessToken(ctx context.Context, accountID uuid.UUID, pair *models.TokenPair) error {
	if pair == nil {
		return fmt.Errorf("token pair is nil")
	}

	var accessJSON []byte
	if pair.AccessToken != nil {
		// Encrypt if not already encrypted
		if len(pair.AccessToken.Ciphertext) > 0 && !s.isEncrypted(pair.AccessToken) {
			plaintext := string(pair.AccessToken.Ciphertext)
			encAccess, err := s.crypto.EncryptToken(ctx, plaintext, pair.AccessToken.KeyID)
			if err != nil {
				return fmt.Errorf("failed to encrypt access token: %w", err)
			}
			pair.AccessToken = encAccess
		}
		var err error
		accessJSON, err = json.Marshal(pair.AccessToken)
		if err != nil {
			return fmt.Errorf("failed to marshal access token: %w", err)
		}
	}

	// If a new refresh token is provided (rotation), encrypt and store it
	var refreshJSON []byte
	if pair.RefreshToken != nil {
		if len(pair.RefreshToken.Ciphertext) > 0 && !s.isEncrypted(pair.RefreshToken) {
			plaintext := string(pair.RefreshToken.Ciphertext)
			encRefresh, err := s.crypto.EncryptToken(ctx, plaintext, pair.RefreshToken.KeyID)
			if err != nil {
				return fmt.Errorf("failed to encrypt refresh token: %w", err)
			}
			pair.RefreshToken = encRefresh
		}
		var err error
		refreshJSON, err = json.Marshal(pair.RefreshToken)
		if err != nil {
			return fmt.Errorf("failed to marshal refresh token: %w", err)
		}
	}

	expiresAt := sql.NullTime{}
	if pair.ExpiresAt != nil {
		expiresAt = sql.NullTime{Time: *pair.ExpiresAt, Valid: true}
	}

	// Build the update query dynamically
	query := "UPDATE email_accounts SET access_token = $1, expires_at = $2, updated_at = NOW()"
	args := []interface{}{accessJSON, expiresAt}
	argIdx := 3

	if len(refreshJSON) > 0 {
		query += fmt.Sprintf(", refresh_token = $%d", argIdx)
		args = append(args, refreshJSON)
		argIdx++
	}

	query += fmt.Sprintf(" WHERE id = $%d", argIdx)
	args = append(args, accountID)

	_, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update access token: %w", err)
	}

	return nil
}

// DeactivateAccount marks an email account as inactive.
// This is called on invalid_grant and other terminal auth errors.
func (s *TokenStore) DeactivateAccount(ctx context.Context, accountID uuid.UUID) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE email_accounts
		SET is_active = false, deactivated_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, accountID)

	if err != nil {
		return fmt.Errorf("failed to deactivate account %s: %w", accountID, err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("account %s not found", accountID)
	}

	return nil
}

// DecryptRefreshToken decrypts the refresh token for use in token refresh operations.
// This should only be called when performing a refresh, never logged.
func (s *TokenStore) DecryptRefreshToken(ctx context.Context, accountID uuid.UUID) (string, error) {
	var refreshJSON []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT refresh_token FROM email_accounts WHERE id = $1 AND is_active = true
	`, accountID).Scan(&refreshJSON)

	if err == sql.ErrNoRows {
		return "", fmt.Errorf("account %s not found or deactivated", accountID)
	}
	if err != nil {
		return "", fmt.Errorf("failed to load refresh token: %w", err)
	}

	if len(refreshJSON) == 0 {
		return "", fmt.Errorf("no refresh token stored for account %s", accountID)
	}

	var encRefresh models.EncryptedToken
	if err := json.Unmarshal(refreshJSON, &encRefresh); err != nil {
		return "", fmt.Errorf("failed to unmarshal refresh token: %w", err)
	}

	plaintext, err := s.crypto.DecryptToken(ctx, &encRefresh)
	if err != nil {
		return "", &models.IngestionError{
			Code:    models.ErrCodeTokenDecryptFailed,
			Message: fmt.Sprintf("failed to decrypt refresh token: %v", err),
			Retry:   true,
		}
	}

	return plaintext, nil
}

// isEncrypted checks if an EncryptedToken is already properly encrypted
// (i.e., has a valid nonce set) vs being a raw token value.
func (s *TokenStore) isEncrypted(enc *models.EncryptedToken) bool {
	if enc == nil {
		return false
	}
	// A properly encrypted token has a nonce (12 bytes for AES-GCM)
	// and a keyID reference set. Raw tokens have empty nonce.
	return len(enc.Nonce) == crypto.NonceSize && enc.KeyID != ""
}

// TokenMetadata holds account metadata without tokens.
type TokenMetadata struct {
	ID         uuid.UUID  `json:"id"`
	Provider   string     `json:"provider"`
	IsActive   bool       `json:"is_active"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// RegisterProvider registers an OAuthProvider for the given provider name.
// This is used by RefreshIfNeeded to route token refresh to the correct provider.
// Must be called at least once for each supported provider ("gmail", "outlook")
// before calling RefreshIfNeeded.
func (s *TokenStore) RegisterProvider(name string, provider models.OAuthProvider) {
	if s.providers == nil {
		s.providers = make(map[string]models.OAuthProvider)
	}
	s.providers[name] = provider
}

// GetTokens retrieves and decrypts the TokenPair for the given account.
// It delegates to LoadTokens — functionally equivalent, provided to satisfy
// the poll.TokenStore interface.
func (s *TokenStore) GetTokens(ctx context.Context, accountID uuid.UUID) (*models.TokenPair, error) {
	return s.LoadTokens(ctx, accountID)
}

// RefreshIfNeeded checks if the access token is valid (not within 5 minutes of
// expiry). If the token is expired or close to expiry, it performs a token
// refresh via the registered OAuth provider, persists the new access token,
// and returns the updated pair.
func (s *TokenStore) RefreshIfNeeded(ctx context.Context, accountID uuid.UUID) (*models.TokenPair, error) {
	pair, err := s.LoadTokens(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("load tokens for refresh check on account %s: %w", accountID, err)
	}

	// Check if token is still valid (not expired and not within 5-min window).
	// AccessTokenPlaintext must be present and ExpiresAt must be > now+5m.
	if pair.AccessTokenPlaintext != nil && pair.ExpiresAt != nil &&
		pair.ExpiresAt.After(time.Now().UTC().Add(5*time.Minute)) {
		return pair, nil
	}

	// Token needs refresh — decrypt the refresh token (secure, never logged).
	refreshToken, err := s.DecryptRefreshToken(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("decrypt refresh token for account %s: %w", accountID, err)
	}
	// Securely wipe refresh token plaintext from memory after use.
	defer crypto.Memzero([]byte(refreshToken))

	// Query the provider name from the database.
	var providerName string
	err = s.db.QueryRowContext(ctx, `
		SELECT provider FROM email_accounts WHERE id = $1
	`, accountID).Scan(&providerName)
	if err != nil {
		return nil, fmt.Errorf("query provider for account %s: %w", accountID, err)
	}

	// Look up the registered provider.
	provider, ok := s.providers[providerName]
	if !ok {
		return nil, fmt.Errorf("no OAuth provider registered for %q (account %s)", providerName, accountID)
	}

	// Call the provider's Refresh endpoint.
	newPair, err := provider.Refresh(ctx, refreshToken)
	if err != nil {
		// Check for invalid_grant — refresh token is permanently expired.
		if strings.Contains(err.Error(), "invalid_grant") {
			// Deactivate the account so polling stops retrying.
			if deactErr := s.DeactivateAccount(ctx, accountID); deactErr != nil {
				return nil, fmt.Errorf("invalid_grant for account %s AND deactivation failed: %v, original: %w", accountID, deactErr, err)
			}
			return nil, &models.IngestionError{
				Code:    models.ErrCodeOAuthExpired,
				Message: fmt.Sprintf("refresh token expired (invalid_grant) for account %s: %v", accountID, err),
				Retry:   false,
			}
		}
		return nil, fmt.Errorf("provider refresh failed for account %s: %w", accountID, err)
	}

	// Encrypt the new access token before persisting.
	// Build the keyID from the provider name and client context.
	keyID := provider.Name() + "-refresh"
	if newPair.AccessToken != nil && len(newPair.AccessToken.Ciphertext) > 0 {
		encAccess, encErr := s.crypto.EncryptToken(ctx, string(newPair.AccessToken.Ciphertext), keyID)
		if encErr != nil {
			return nil, fmt.Errorf("encrypt new access token for account %s: %w", accountID, encErr)
		}
		newPair.AccessToken = encAccess
	}

	// If the provider returned a new refresh token (token rotation), encrypt it too.
	if newPair.RefreshToken != nil && len(newPair.RefreshToken.Ciphertext) > 0 {
		encRefresh, encErr := s.crypto.EncryptToken(ctx, string(newPair.RefreshToken.Ciphertext), keyID)
		if encErr != nil {
			return nil, fmt.Errorf("encrypt new refresh token for account %s: %w", accountID, encErr)
		}
		newPair.RefreshToken = encRefresh
	}

	// Persist the updated tokens.
	if updErr := s.UpdateAccessToken(ctx, accountID, newPair); updErr != nil {
		return nil, fmt.Errorf("persist refreshed tokens for account %s: %w", accountID, updErr)
	}

	return newPair, nil
}

// ListActiveAccounts returns metadata for all active accounts of a given provider.
func (s *TokenStore) ListActiveAccounts(ctx context.Context, provider string) ([]TokenMetadata, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, provider, is_active, created_at, updated_at, expires_at
		FROM email_accounts
		WHERE provider = $1 AND is_active = true
		ORDER BY updated_at DESC
	`, provider)
	if err != nil {
		return nil, fmt.Errorf("failed to list accounts: %w", err)
	}
	defer rows.Close()

	var accounts []TokenMetadata
	for rows.Next() {
		var meta TokenMetadata
		var expiresAt sql.NullTime
		err := rows.Scan(&meta.ID, &meta.Provider, &meta.IsActive, &meta.CreatedAt, &meta.UpdatedAt, &expiresAt)
		if err != nil {
			continue
		}
		if expiresAt.Valid {
			meta.ExpiresAt = &expiresAt.Time
		}
		accounts = append(accounts, meta)
	}

	return accounts, rows.Err()
}
