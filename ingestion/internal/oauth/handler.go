// Package oauth provides HTTP handlers for the OAuth 2.0 authorization flows.
package oauth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/decisionstack/ingestion/internal/backfill"
	"github.com/decisionstack/ingestion/internal/config"
	"github.com/decisionstack/ingestion/internal/crypto"
	"github.com/decisionstack/ingestion/internal/models"
)

// RedisClient defines the minimal Redis interface needed for OAuth state management.
// Implemented by *redis.Client from github.com/redis/go-redis/v9.
type RedisClient interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	SetEX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
}

// NATSPublisher defines the minimal NATS interface needed for publishing events.
type NATSPublisher interface {
	Publish(subject string, data []byte) error
}

// FrontendRedirectFunc builds the frontend redirect URL after OAuth callback.
type FrontendRedirectFunc func(state OAuthCallbackState) string

// OAuthCallbackState is passed to the frontend redirect after callback processing.
type OAuthCallbackState struct {
	Success     bool      `json:"success"`
	AccountID   uuid.UUID `json:"account_id,omitempty"`
	Provider    string    `json:"provider"`
	Email       string    `json:"email,omitempty"`
	Error       string    `json:"error,omitempty"`
	ErrorCode   string    `json:"error_code,omitempty"`
	NeedsReauth bool      `json:"needs_reauth,omitempty"`
}

// OAuthHandler mounts HTTP routes for OAuth flows.
type OAuthHandler struct {
	providers      map[ProviderName]models.OAuthProvider
	tokenCrypto    *crypto.TokenCrypto
	db             *sql.DB
	redis          RedisClient
	nats           NATSPublisher
	cfg            *config.Config
	frontendURL    string
	getRedirectURL FrontendRedirectFunc
}

// NewOAuthHandler creates a new OAuthHandler with all required dependencies.
func NewOAuthHandler(
	cfg *config.Config,
	tokenCrypto *crypto.TokenCrypto,
	db *sql.DB,
	redis RedisClient,
	nats NATSPublisher,
	frontendURL string,
) *OAuthHandler {
	h := &OAuthHandler{
		providers:   make(map[ProviderName]models.OAuthProvider),
		tokenCrypto: tokenCrypto,
		db:          db,
		redis:       redis,
		nats:        nats,
		cfg:         cfg,
		frontendURL: frontendURL,
	}

	// Initialize providers
	for _, name := range ProviderNames() {
		provider, _ := NewProvider(name, cfg)
		h.providers[name] = provider
	}

	// Default redirect URL builder
	h.getRedirectURL = func(state OAuthCallbackState) string {
		data, _ := json.Marshal(state)
		return fmt.Sprintf("%s/oauth/callback?state=%s", frontendURL,
			base64.URLEncoding.EncodeToString(data))
	}

	return h
}

// NewHandler is a convenience alias for NewOAuthHandler.
// It uses the frontend URL from configuration.
func NewHandler(cfg *config.Config, db *sql.DB, redis RedisClient, tokenCrypto *crypto.TokenCrypto, nats NATSPublisher) *OAuthHandler {
	frontendURL := cfg.GoogleRedirectURI
	if len(frontendURL) > 0 {
		// Strip the callback path to get the base frontend URL
		frontendURL = frontendURL[:len(frontendURL)-len("/auth/google/callback")]
	}
	if frontendURL == "" {
		frontendURL = "http://localhost:3000"
	}
	return NewOAuthHandler(cfg, tokenCrypto, db, redis, nats, frontendURL)
}

// SetRedirectFunc allows overriding the default redirect URL builder.
func (h *OAuthHandler) SetRedirectFunc(fn FrontendRedirectFunc) {
	h.getRedirectURL = fn
}

// ---------------------------------------------------------------------------
// Route Registration
// ---------------------------------------------------------------------------

// RegisterRoutes mounts OAuth routes on the given chi.Router.
//
// Routes:
//   GET  /auth/{provider}           - Initiate OAuth flow
//   GET  /auth/{provider}/callback  - Handle OAuth callback
//   POST /auth/{provider}/refresh   - Refresh access token
//   POST /auth/{provider}/revoke    - Revoke tokens
func (h *OAuthHandler) RegisterRoutes(r chi.Router) {
	r.Get("/auth/{provider}", h.handleAuthInit)
	r.Get("/auth/{provider}/callback", h.handleAuthCallback)
	r.Post("/auth/{provider}/refresh", h.handleAuthRefresh)
	r.Post("/auth/{provider}/revoke", h.handleAuthRevoke)
}

// Routes returns a chi.Router with all OAuth routes mounted.
// Use this to Mount("/auth", authHandler.Routes()).
func (h *OAuthHandler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/{provider}", h.handleAuthInit)
	r.Get("/{provider}/callback", h.handleAuthCallback)
	r.Post("/{provider}/refresh", h.handleAuthRefresh)
	r.Post("/{provider}/revoke", h.handleAuthRevoke)
	return r
}

// ---------------------------------------------------------------------------
// GET /auth/{provider} — Initiate OAuth flow
// ---------------------------------------------------------------------------

func (h *OAuthHandler) handleAuthInit(w http.ResponseWriter, r *http.Request) {
	providerName := chi.URLParam(r, "provider")
	if !IsValidProvider(providerName) {
		http.Error(w, fmt.Sprintf("Unsupported provider: %s", providerName), http.StatusBadRequest)
		return
	}

	provider := h.providers[ProviderName(providerName)]

	// Generate random 32-byte state parameter
	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		http.Error(w, "Failed to generate state", http.StatusInternalServerError)
		return
	}
	state := hex.EncodeToString(stateBytes)

	// Store state in Redis with 10-minute TTL
	stateKey := fmt.Sprintf("oauth:state:%s", state)
	stateValue := fmt.Sprintf("%s:%s", providerName, time.Now().Unix())
	if err := h.redis.SetEX(r.Context(), stateKey, stateValue, 10*time.Minute).Err(); err != nil {
		http.Error(w, "Failed to store state", http.StatusInternalServerError)
		return
	}

	// Build the OAuth URL
	redirectURI := h.buildRedirectURI(providerName)
	authURL := provider.AuthURL(state, redirectURI)

	http.Redirect(w, r, authURL, http.StatusFound)
}

// ---------------------------------------------------------------------------
// GET /auth/{provider}/callback — Handle OAuth callback
// ---------------------------------------------------------------------------

func (h *OAuthHandler) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	providerName := chi.URLParam(r, "provider")
	if !IsValidProvider(providerName) {
		h.redirectWithError(w, r, providerName, "unsupported provider", "unsupported_provider", false)
		return
	}

	// Verify state parameter
	state := r.URL.Query().Get("state")
	if state == "" {
		h.redirectWithError(w, r, providerName, "missing state parameter", "missing_state", false)
		return
	}

	stateKey := fmt.Sprintf("oauth:state:%s", state)
	storedValue, err := h.redis.Get(ctx, stateKey).Result()
	if err != nil {
		h.redirectWithError(w, r, providerName, "invalid or expired state", "invalid_state", false)
		return
	}

	// Validate state matches provider
	expectedPrefix := providerName + ":"
	if !strings.HasPrefix(storedValue, expectedPrefix) {
		h.redirectWithError(w, r, providerName, "state mismatch", "state_mismatch", false)
		return
	}

	// Delete state immediately (single-use)
	_ = h.redis.Del(ctx, stateKey).Err()

	// Check for OAuth error from provider
	if oauthErr := r.URL.Query().Get("error"); oauthErr != "" {
		h.redirectWithError(w, r, providerName, oauthErr, oauthErr, oauthErr == "access_denied")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		h.redirectWithError(w, r, providerName, "missing authorization code", "missing_code", false)
		return
	}

	// Exchange code for tokens
	provider := h.providers[ProviderName(providerName)]
	redirectURI := h.buildRedirectURI(providerName)

	tokenPair, err := provider.Exchange(ctx, code, redirectURI)
	if err != nil {
		h.handleExchangeError(w, r, providerName, err)
		return
	}

	// Encrypt tokens using TokenCrypto
	keyID := h.cfg.KMSKeyID

	// Encrypt refresh token
	if tokenPair.RefreshToken != nil && len(tokenPair.RefreshToken.Ciphertext) > 0 {
		refreshPlaintext := string(tokenPair.RefreshToken.Ciphertext)
		encRefresh, err := h.tokenCrypto.EncryptToken(ctx, refreshPlaintext, keyID)
		if err != nil {
			h.redirectWithError(w, r, providerName, "failed to encrypt refresh token", "encrypt_error", false)
			return
		}
		tokenPair.RefreshToken = encRefresh
	}

	// Encrypt access token
	if tokenPair.AccessToken != nil && len(tokenPair.AccessToken.Ciphertext) > 0 {
		accessPlaintext := string(tokenPair.AccessToken.Ciphertext)
		encAccess, err := h.tokenCrypto.EncryptToken(ctx, accessPlaintext, keyID)
		if err != nil {
			h.redirectWithError(w, r, providerName, "failed to encrypt access token", "encrypt_error", false)
			return
		}
		tokenPair.AccessToken = encAccess
	}

	// Extract email from the token (using the access token)
	email := ""
	if tokenPair.AccessTokenPlaintext != nil {
		email = h.extractEmailFromToken(ctx, provider, *tokenPair.AccessTokenPlaintext)
	}

	// Insert into email_accounts table
	accountID := uuid.Must(uuid.NewRandom())
	expiresAt := sql.NullTime{}
	if tokenPair.ExpiresAt != nil {
		expiresAt = sql.NullTime{Time: *tokenPair.ExpiresAt, Valid: true}
	}

	refreshJSON, _ := json.Marshal(tokenPair.RefreshToken)
	accessJSON, _ := json.Marshal(tokenPair.AccessToken)

	_, err = h.db.ExecContext(ctx, `
		INSERT INTO email_accounts (
			id, provider, email_address, refresh_token, access_token,
			expires_at, scope_granted, is_active, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, true, NOW())
		ON CONFLICT (email_address, provider) DO UPDATE SET
			refresh_token = EXCLUDED.refresh_token,
			access_token = EXCLUDED.access_token,
			expires_at = EXCLUDED.expires_at,
			scope_granted = EXCLUDED.scope_granted,
			is_active = true,
			updated_at = NOW()
	`, accountID, providerName, email, refreshJSON, accessJSON, expiresAt,
		strings.Join(tokenPair.ScopeGranted, ","))

	if err != nil {
		h.redirectWithError(w, r, providerName, "failed to persist tokens", "db_error", false)
		return
	}

	// Trigger historical backfill after successful OAuth
	// We need the user_id. For new accounts, derive from context or use a lookup.
	// The account was just created/updated — look up the user_id.
	var userID uuid.UUID
	_ = h.db.QueryRowContext(ctx, `SELECT user_id FROM email_accounts WHERE id = $1`, accountID).Scan(&userID)

	if userID != uuid.Nil {
		// Extract historyId from provider response if available (Gmail only)
		var historyID string
		if providerName == "gmail" {
			// The Gmail OAuth callback response may include a historyId
			// This is extracted from the token response or via a separate API call
			// For now, leave empty — the backfill worker will do a full sync
			historyID = ""
		}

		// Type assert to *redis.Client — the actual value is always *redis.Client
		if redisClient, ok := h.redis.(*redis.Client); ok {
			backfill.TriggerFromCallback(ctx, redisClient, userID, accountID, providerName, historyID, slog.Default())
		}
	}

	// Redirect to frontend with success
	callbackState := OAuthCallbackState{
		Success:   true,
		AccountID: accountID,
		Provider:  providerName,
		Email:     email,
	}

	http.Redirect(w, r, h.getRedirectURL(callbackState), http.StatusFound)
}

// ---------------------------------------------------------------------------
// POST /auth/{provider}/refresh — Refresh access token
// ---------------------------------------------------------------------------

func (h *OAuthHandler) handleAuthRefresh(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	providerName := chi.URLParam(r, "provider")
	if !IsValidProvider(providerName) {
		http.Error(w, "Unsupported provider", http.StatusBadRequest)
		return
	}

	// Parse request body for account ID
	var req struct {
		AccountID uuid.UUID `json:"account_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Load tokens from storage
	var refreshJSON, accessJSON []byte
	var isActive bool
	err := h.db.QueryRowContext(ctx, `
		SELECT refresh_token, access_token, is_active
		FROM email_accounts WHERE id = $1 AND provider = $2
	`, req.AccountID, providerName).Scan(&refreshJSON, &accessJSON, &isActive)

	if err == sql.ErrNoRows {
		http.Error(w, "Account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	if !isActive {
		http.Error(w, "Account is deactivated, re-authentication required", http.StatusForbidden)
		return
	}

	// Decrypt refresh token
	var encRefresh models.EncryptedToken
	if err := json.Unmarshal(refreshJSON, &encRefresh); err != nil {
		http.Error(w, "Corrupted refresh token", http.StatusInternalServerError)
		return
	}

	refreshToken, err := h.tokenCrypto.DecryptToken(ctx, &encRefresh)
	if err != nil {
		http.Error(w, "Failed to decrypt refresh token", http.StatusInternalServerError)
		return
	}

	// Call provider refresh
	provider := h.providers[ProviderName(providerName)]
	newPair, err := provider.Refresh(ctx, refreshToken)
	if err != nil {
		// Check for invalid_grant
		if ingestErr, ok := err.(models.IngestionError); ok && ingestErr.Code == models.ErrCodeOAuthExpired {
			// Mark account inactive
			_ = h.deactivateAccount(ctx, req.AccountID)
			// Publish re-auth card notification
			_ = h.publishReauthCard(req.AccountID, providerName)
			http.Error(w, "Token expired, account deactivated. Please re-authenticate.", http.StatusForbidden)
			return
		}
		http.Error(w, "Refresh failed", http.StatusInternalServerError)
		return
	}

	// Encrypt new tokens
	keyID := h.cfg.KMSKeyID

	if newPair.RefreshToken != nil && len(newPair.RefreshToken.Ciphertext) > 0 {
		refreshPlaintext := string(newPair.RefreshToken.Ciphertext)
		encRefresh, err := h.tokenCrypto.EncryptToken(ctx, refreshPlaintext, keyID)
		if err != nil {
			http.Error(w, "Failed to encrypt refresh token", http.StatusInternalServerError)
			return
		}
		newPair.RefreshToken = encRefresh
	}

	if newPair.AccessToken != nil && len(newPair.AccessToken.Ciphertext) > 0 {
		accessPlaintext := string(newPair.AccessToken.Ciphertext)
		encAccess, err := h.tokenCrypto.EncryptToken(ctx, accessPlaintext, keyID)
		if err != nil {
			http.Error(w, "Failed to encrypt access token", http.StatusInternalServerError)
			return
		}
		newPair.AccessToken = encAccess
	}

	// Update database with new tokens
	newRefreshJSON, _ := json.Marshal(newPair.RefreshToken)
	newAccessJSON, _ := json.Marshal(newPair.AccessToken)
	expiresAt := sql.NullTime{}
	if newPair.ExpiresAt != nil {
		expiresAt = sql.NullTime{Time: *newPair.ExpiresAt, Valid: true}
	}

	_, err = h.db.ExecContext(ctx, `
		UPDATE email_accounts
		SET refresh_token = $1, access_token = $2, expires_at = $3, updated_at = NOW()
		WHERE id = $4 AND provider = $5
	`, newRefreshJSON, newAccessJSON, expiresAt, req.AccountID, providerName)

	if err != nil {
		http.Error(w, "Failed to persist new tokens", http.StatusInternalServerError)
		return
	}

	// Return the new access token plaintext for in-memory use (15-min TTL)
	resp := map[string]interface{}{
		"access_token": newPair.AccessTokenPlaintext,
		"expires_at":   newPair.ExpiresAt,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ---------------------------------------------------------------------------
// POST /auth/{provider}/revoke — Revoke tokens
// ---------------------------------------------------------------------------

func (h *OAuthHandler) handleAuthRevoke(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	providerName := chi.URLParam(r, "provider")
	if !IsValidProvider(providerName) {
		http.Error(w, "Unsupported provider", http.StatusBadRequest)
		return
	}

	var req struct {
		AccountID uuid.UUID `json:"account_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Load and decrypt tokens
	var refreshJSON []byte
	err := h.db.QueryRowContext(ctx, `
		SELECT refresh_token FROM email_accounts WHERE id = $1 AND provider = $2
	`, req.AccountID, providerName).Scan(&refreshJSON)

	if err == sql.ErrNoRows {
		http.Error(w, "Account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	var encRefresh models.EncryptedToken
	if err := json.Unmarshal(refreshJSON, &encRefresh); err != nil {
		http.Error(w, "Corrupted token", http.StatusInternalServerError)
		return
	}

	refreshToken, err := h.tokenCrypto.DecryptToken(ctx, &encRefresh)
	if err != nil {
		http.Error(w, "Failed to decrypt token", http.StatusInternalServerError)
		return
	}

	// Revoke via provider
	provider := h.providers[ProviderName(providerName)]
	if err := provider.Revoke(ctx, refreshToken); err != nil {
		// Log but continue - we still want to deactivate locally
	}

	// Mark account inactive
	if err := h.deactivateAccount(ctx, req.AccountID); err != nil {
		http.Error(w, "Failed to deactivate account", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Helper methods
// ---------------------------------------------------------------------------

// buildRedirectURI returns the pre-configured callback redirect URI for a provider.
// Uses the redirect URIs defined in the application configuration.
func (h *OAuthHandler) buildRedirectURI(providerName string) string {
	switch ProviderName(providerName) {
	case ProviderGmail:
		return h.cfg.GoogleRedirectURI
	case ProviderOutlook:
		return h.cfg.MicrosoftRedirectURI
	default:
		return ""
	}
}

// handleExchangeError handles errors from the token exchange, including
// the critical invalid_grant case which requires account deactivation.
func (h *OAuthHandler) handleExchangeError(w http.ResponseWriter, r *http.Request, providerName string, err error) {
	// Check for invalid_grant
	if ingestErr, ok := err.(models.IngestionError); ok {
		switch ingestErr.Code {
		case models.ErrCodeOAuthExpired:
			h.redirectWithError(w, r, providerName, ingestErr.Message, "invalid_grant", true)
			return
		}
	}

	if strings.Contains(err.Error(), "invalid_grant") {
		h.redirectWithError(w, r, providerName, "Authorization expired or revoked", "invalid_grant", true)
		return
	}

	h.redirectWithError(w, r, providerName, err.Error(), "exchange_failed", false)
}

// redirectWithError redirects to the frontend with an error state.
func (h *OAuthHandler) redirectWithError(w http.ResponseWriter, r *http.Request, providerName, errMsg, errCode string, needsReauth bool) {
	state := OAuthCallbackState{
		Success:     false,
		Provider:    providerName,
		Error:       errMsg,
		ErrorCode:   errCode,
		NeedsReauth: needsReauth,
	}
	http.Redirect(w, r, h.getRedirectURL(state), http.StatusFound)
}

// deactivateAccount marks an email account as inactive.
func (h *OAuthHandler) deactivateAccount(ctx context.Context, accountID uuid.UUID) error {
	_, err := h.db.ExecContext(ctx, `
		UPDATE email_accounts
		SET is_active = false, deactivated_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, accountID)
	return err
}

// publishReauthCard publishes a NATS event to create a re-authentication card.
func (h *OAuthHandler) publishReauthCard(accountID uuid.UUID, providerName string) error {
	if h.nats == nil {
		return nil
	}

	event := map[string]interface{}{
		"event_type":   "reauth_required",
		"account_id":   accountID,
		"provider":     providerName,
		"reason":       "invalid_grant",
		"created_at":   time.Now().UTC(),
	}

	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	return h.nats.Publish(models.SubjectCardCreated, data)
}

// extractEmailFromToken extracts the user's email from an access token.
func (h *OAuthHandler) extractEmailFromToken(ctx context.Context, provider models.OAuthProvider, accessToken string) string {
	// Use a lightweight API call to get user info
	switch provider.Name() {
	case string(ProviderGmail):
		return h.extractGoogleEmail(ctx, accessToken)
	case string(ProviderOutlook):
		return h.extractMicrosoftEmail(ctx, accessToken)
	}
	return ""
}

// extractGoogleEmail fetches the user's email from the Google UserInfo API.
func (h *OAuthHandler) extractGoogleEmail(ctx context.Context, accessToken string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleUserInfoURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var result struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}
	return result.Email
}

// extractMicrosoftEmail fetches the user's email from the Microsoft Graph API.
func (h *OAuthHandler) extractMicrosoftEmail(ctx context.Context, accessToken string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, msGraphBaseURL+"/me", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var result struct {
		Mail  string `json:"mail"`
		Email string `json:"userPrincipalName"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}
	if result.Mail != "" {
		return result.Mail
	}
	return result.Email
}


