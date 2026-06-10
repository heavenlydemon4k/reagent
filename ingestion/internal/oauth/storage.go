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
	"github.com/lib/pq"

	"github.com/decisionstack/ingestion/internal/crypto"
	"github.com/decisionstack/ingestion/internal/models"
)

// TokenStore handles persistence of encrypted OAuth tokens in PostgreSQL.
// All token fields are encrypted at rest using AES-256-GCM with KMS-managed DEKs.
type TokenStore struct {
	db        *sql.DB
	crypto    *crypto.TokenCrypto
	providers map[string]models.OAuthProvider
}

// NewTokenStore creates a new TokenStore.
func NewTokenStore(db *sql.DB, crypto *crypto.TokenCrypto) *TokenStore {
	return &TokenStore{
		db:     db,
		crypto: crypto,
	}
}

// UpsertAccountWithTokens creates or updates an email_accounts row with encrypted tokens.
// This is the entry point called from the OAuth callback after code exchange.
// On conflict (user_id, email_address) it updates the token columns in place.
func (s *TokenStore) UpsertAccountWithTokens(
	ctx context.Context,
	userID uuid.UUID,
	provider string,
	emailAddress string,
	pair *models.TokenPair,
) (uuid.UUID, error) {
	if pair == nil {
		return uuid.Nil, fmt.Errorf("token pair is nil")
	}

	refreshJSON, err := s.encryptAndMarshal(ctx, pair.RefreshToken)
	if err != nil {
		return uuid.Nil, fmt.Errorf("encrypt refresh token: %w", err)
	}

	accessJSON, err := s.encryptAndMarshal(ctx, pair.AccessToken)
	if err != nil {
		return uuid.Nil, fmt.Errorf("encrypt access token: %w", err)
	}

	expiresAt := sql.NullTime{}
	if pair.ExpiresAt != nil {
		expiresAt = sql.NullTime{Time: *pair.ExpiresAt, Valid: true}
	}

	accountID := uuid.New()
	var resultID uuid.UUID
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO email_accounts (
			id, user_id, provider, email_address,
			refresh_token_enc, access_token_enc, token_expires_at,
			scope_granted, is_active
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, true)
		ON CONFLICT (user_id, email_address) DO UPDATE SET
			refresh_token_enc  = EXCLUDED.refresh_token_enc,
			access_token_enc   = EXCLUDED.access_token_enc,
			token_expires_at   = EXCLUDED.token_expires_at,
			scope_granted      = EXCLUDED.scope_granted,
			is_active          = true,
			updated_at         = NOW()
		RETURNING id
	`, accountID, userID, provider, emailAddress,
		refreshJSON, accessJSON, expiresAt,
		pq.Array(pair.ScopeGranted),
	).Scan(&resultID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("upsert account for %s/%s: %w", provider, emailAddress, err)
	}

	return resultID, nil
}

// SaveTokens updates token columns for an existing account row identified by accountID.
// Both refresh and access tokens are encrypted before storage.
func (s *TokenStore) SaveTokens(ctx context.Context, accountID uuid.UUID, pair *models.TokenPair) error {
	if pair == nil {
		return fmt.Errorf("token pair is nil")
	}

	refreshJSON, err := s.encryptAndMarshal(ctx, pair.RefreshToken)
	if err != nil {
		return fmt.Errorf("encrypt refresh token: %w", err)
	}

	accessJSON, err := s.encryptAndMarshal(ctx, pair.AccessToken)
	if err != nil {
		return fmt.Errorf("encrypt access token: %w", err)
	}

	expiresAt := sql.NullTime{}
	if pair.ExpiresAt != nil {
		expiresAt = sql.NullTime{Time: *pair.ExpiresAt, Valid: true}
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE email_accounts SET
			refresh_token_enc = $2,
			access_token_enc  = $3,
			token_expires_at  = $4,
			scope_granted     = $5,
			is_active         = true,
			updated_at        = NOW()
		WHERE id = $1
	`, accountID, refreshJSON, accessJSON, expiresAt, pq.Array(pair.ScopeGranted))
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
	var scopes pq.StringArray
	var isActive bool

	err := s.db.QueryRowContext(ctx, `
		SELECT refresh_token_enc, access_token_enc, token_expires_at, scope_granted, is_active
		FROM email_accounts WHERE id = $1
	`, accountID).Scan(&refreshJSON, &accessJSON, &expiresAt, &scopes, &isActive)

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

	pair := &models.TokenPair{
		ScopeGranted: []string(scopes),
	}

	if len(refreshJSON) > 0 {
		var encRefresh models.EncryptedToken
		if err := json.Unmarshal(refreshJSON, &encRefresh); err != nil {
			return nil, fmt.Errorf("failed to unmarshal refresh token: %w", err)
		}
		pair.RefreshToken = &encRefresh
	}

	if len(accessJSON) > 0 {
		var encAccess models.EncryptedToken
		if err := json.Unmarshal(accessJSON, &encAccess); err != nil {
			return nil, fmt.Errorf("failed to unmarshal access token: %w", err)
		}
		pair.AccessToken = &encAccess

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

	return pair, nil
}

// UpdateAccessToken updates only the access token (used after refresh).
// If a new refresh token is provided (token rotation), it is encrypted and stored too.
func (s *TokenStore) UpdateAccessToken(ctx context.Context, accountID uuid.UUID, pair *models.TokenPair) error {
	if pair == nil {
		return fmt.Errorf("token pair is nil")
	}

	accessJSON, err := s.encryptAndMarshal(ctx, pair.AccessToken)
	if err != nil {
		return fmt.Errorf("encrypt access token: %w", err)
	}

	refreshJSON, err := s.encryptAndMarshal(ctx, pair.RefreshToken)
	if err != nil {
		return fmt.Errorf("encrypt refresh token: %w", err)
	}

	expiresAt := sql.NullTime{}
	if pair.ExpiresAt != nil {
		expiresAt = sql.NullTime{Time: *pair.ExpiresAt, Valid: true}
	}

	query := "UPDATE email_accounts SET access_token_enc = $1, token_expires_at = $2, updated_at = NOW()"
	args := []interface{}{accessJSON, expiresAt}
	argIdx := 3

	if len(refreshJSON) > 0 {
		query += fmt.Sprintf(", refresh_token_enc = $%d", argIdx)
		args = append(args, refreshJSON)
		argIdx++
	}

	query += fmt.Sprintf(" WHERE id = $%d", argIdx)
	args = append(args, accountID)

	_, err = s.db.ExecContext(ctx, query, args...)
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
		SELECT refresh_token_enc FROM email_accounts WHERE id = $1 AND is_active = true
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
	return len(enc.Nonce) == crypto.NonceSize && enc.KeyID != ""
}

// encryptAndMarshal encrypts a raw token if needed, then JSON-marshals the result.
// Returns nil, nil if enc is nil or has no ciphertext.
func (s *TokenStore) encryptAndMarshal(ctx context.Context, enc *models.EncryptedToken) ([]byte, error) {
	if enc == nil || len(enc.Ciphertext) == 0 {
		return nil, nil
	}
	if !s.isEncrypted(enc) {
		plaintext := string(enc.Ciphertext)
		encrypted, err := s.crypto.EncryptToken(ctx, plaintext, enc.KeyID)
		if err != nil {
			return nil, err
		}
		enc = encrypted
	}
	return json.Marshal(enc)
}

// TokenMetadata holds account metadata without tokens.
type TokenMetadata struct {
	ID        uuid.UUID  `json:"id"`
	Provider  string     `json:"provider"`
	IsActive  bool       `json:"is_active"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// RegisterProvider registers an OAuthProvider for the given provider name.
func (s *TokenStore) RegisterProvider(name string, provider models.OAuthProvider) {
	if s.providers == nil {
		s.providers = make(map[string]models.OAuthProvider)
	}
	s.providers[name] = provider
}

// GetTokens retrieves and decrypts the TokenPair for the given account.
func (s *TokenStore) GetTokens(ctx context.Context, accountID uuid.UUID) (*models.TokenPair, error) {
	return s.LoadTokens(ctx, accountID)
}

// RefreshIfNeeded checks if the access token is valid. If expired or near expiry,
// it performs a token refresh via the registered OAuth provider and persists the result.
func (s *TokenStore) RefreshIfNeeded(ctx context.Context, accountID uuid.UUID) (*models.TokenPair, error) {
	pair, err := s.LoadTokens(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("load tokens for refresh check on account %s: %w", accountID, err)
	}

	if pair.AccessTokenPlaintext != nil && pair.ExpiresAt != nil &&
		pair.ExpiresAt.After(time.Now().UTC().Add(5*time.Minute)) {
		return pair, nil
	}

	refreshToken, err := s.DecryptRefreshToken(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("decrypt refresh token for account %s: %w", accountID, err)
	}
	defer crypto.Memzero([]byte(refreshToken))

	var providerName string
	err = s.db.QueryRowContext(ctx, `
		SELECT provider FROM email_accounts WHERE id = $1
	`, accountID).Scan(&providerName)
	if err != nil {
		return nil, fmt.Errorf("query provider for account %s: %w", accountID, err)
	}

	provider, ok := s.providers[providerName]
	if !ok {
		return nil, fmt.Errorf("no OAuth provider registered for %q (account %s)", providerName, accountID)
	}

	newPair, err := provider.Refresh(ctx, refreshToken)
	if err != nil {
		if strings.Contains(err.Error(), "invalid_grant") {
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

	keyID := provider.Name() + "-refresh"
	if newPair.AccessToken != nil && len(newPair.AccessToken.Ciphertext) > 0 {
		encAccess, encErr := s.crypto.EncryptToken(ctx, string(newPair.AccessToken.Ciphertext), keyID)
		if encErr != nil {
			return nil, fmt.Errorf("encrypt new access token for account %s: %w", accountID, encErr)
		}
		newPair.AccessToken = encAccess
	}

	if newPair.RefreshToken != nil && len(newPair.RefreshToken.Ciphertext) > 0 {
		encRefresh, encErr := s.crypto.EncryptToken(ctx, string(newPair.RefreshToken.Ciphertext), keyID)
		if encErr != nil {
			return nil, fmt.Errorf("encrypt new refresh token for account %s: %w", accountID, encErr)
		}
		newPair.RefreshToken = encRefresh
	}

	if updErr := s.UpdateAccessToken(ctx, accountID, newPair); updErr != nil {
		return nil, fmt.Errorf("persist refreshed tokens for account %s: %w", accountID, updErr)
	}

	return newPair, nil
}

// ListActiveAccounts returns metadata for all active accounts of a given provider.
func (s *TokenStore) ListActiveAccounts(ctx context.Context, provider string) ([]TokenMetadata, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, provider, is_active, created_at, updated_at, token_expires_at
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
