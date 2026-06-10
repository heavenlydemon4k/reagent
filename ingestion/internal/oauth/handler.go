package oauth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/decisionstack/ingestion/internal/config"
	"github.com/decisionstack/ingestion/internal/models"
)

// SuccessCallback inverts the dependency: oauth knows nothing about backfill.
type SuccessCallback func(ctx context.Context, userID uuid.UUID) error

// Handler implements the OAuth 2.0 login and callback flows for Google and Microsoft.
// It stores CSRF state in Redis (TTL 10 min) and writes encrypted tokens to Postgres
// via TokenStore.UpsertAccountWithTokens.
type Handler struct {
	db             *sql.DB
	log            *slog.Logger
	tokenStore     *TokenStore
	cfg            *config.Config
	redis          *goredis.Client
	googleProvider models.OAuthProvider
	msftProvider   models.OAuthProvider
	onSuccess      SuccessCallback
}

// NewHandler builds the OAuth handler. Provider instances are only created when
// the corresponding credentials are present in cfg — callers can operate without
// OAuth credentials in dev mode (routes return 503).
func NewHandler(
	db *sql.DB,
	log *slog.Logger,
	tokenStore *TokenStore,
	cfg *config.Config,
	rdb *goredis.Client,
	onSuccess SuccessCallback,
) *Handler {
	h := &Handler{
		db:         db,
		log:        log,
		tokenStore: tokenStore,
		cfg:        cfg,
		redis:      rdb,
		onSuccess:  onSuccess,
	}
	if cfg.GoogleClientID != "" && cfg.GoogleClientSecret != "" {
		h.googleProvider = newGoogleProvider(cfg)
	}
	if cfg.MicrosoftClientID != "" && cfg.MicrosoftClientSecret != "" {
		h.msftProvider = newMicrosoftProvider(cfg)
	}
	return h
}

// Routes returns a chi sub-router mounted at /auth by the parent router.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Get("/google", h.HandleGoogleLogin)
	r.Get("/google/callback", h.HandleGoogleCallback)
	r.Get("/microsoft", h.HandleMicrosoftLogin)
	r.Get("/microsoft/callback", h.HandleMicrosoftCallback)
	return r
}

// HandleGoogleLogin initiates the Google OAuth flow by generating a CSRF state,
// storing it in Redis, and redirecting to Google's authorization endpoint.
func (h *Handler) HandleGoogleLogin(w http.ResponseWriter, r *http.Request) {
	if h.googleProvider == nil {
		jsonError(w, "google_oauth_not_configured", http.StatusServiceUnavailable)
		return
	}
	state, err := generateState()
	if err != nil {
		h.log.Error("failed to generate oauth state", "error", err)
		jsonError(w, "internal_error", http.StatusInternalServerError)
		return
	}
	if err := h.redis.SetEx(r.Context(), stateKey(state), "google", 10*time.Minute).Err(); err != nil {
		h.log.Error("failed to store oauth state in redis", "error", err)
		jsonError(w, "internal_error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, h.googleProvider.AuthURL(state, h.cfg.GoogleRedirectURI), http.StatusFound)
}

// HandleGoogleCallback validates state, exchanges the code, fetches user info,
// upserts the user + email account row, stores encrypted tokens, and fires onSuccess.
func (h *Handler) HandleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	if h.googleProvider == nil {
		jsonError(w, "google_oauth_not_configured", http.StatusServiceUnavailable)
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.log.Warn("google oauth denied by user", "error", errParam)
		jsonError(w, "oauth_denied", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		jsonError(w, "missing_code_or_state", http.StatusBadRequest)
		return
	}

	if err := h.validateAndConsumeState(r.Context(), state, "google"); err != nil {
		h.log.Warn("google oauth invalid state", "error", err)
		jsonError(w, "invalid_state", http.StatusBadRequest)
		return
	}

	pair, err := h.googleProvider.Exchange(r.Context(), code, h.cfg.GoogleRedirectURI)
	if err != nil {
		h.log.Error("google token exchange failed", "error", err)
		jsonError(w, "token_exchange_failed", http.StatusBadGateway)
		return
	}

	if pair.AccessTokenPlaintext == nil {
		h.log.Error("google exchange returned no access token plaintext")
		jsonError(w, "token_exchange_failed", http.StatusBadGateway)
		return
	}

	email, name, err := h.fetchGoogleUserEmail(r.Context(), *pair.AccessTokenPlaintext)
	if err != nil {
		h.log.Error("failed to fetch google user info", "error", err)
		jsonError(w, "userinfo_failed", http.StatusBadGateway)
		return
	}

	userID, err := h.upsertUser(r.Context(), email, name)
	if err != nil {
		h.log.Error("failed to upsert user", "email", email, "error", err)
		jsonError(w, "internal_error", http.StatusInternalServerError)
		return
	}

	accountID, err := h.tokenStore.UpsertAccountWithTokens(r.Context(), userID, string(ProviderGmail), email, pair)
	if err != nil {
		h.log.Error("failed to save google tokens", "user_id", userID, "error", err)
		jsonError(w, "token_storage_failed", http.StatusInternalServerError)
		return
	}

	h.log.Info("google oauth complete", "user_id", userID, "account_id", accountID, "email", email)
	h.fireOnSuccess(r.Context(), userID)
	http.Redirect(w, r, "/", http.StatusFound)
}

// HandleMicrosoftLogin initiates the Microsoft OAuth flow.
func (h *Handler) HandleMicrosoftLogin(w http.ResponseWriter, r *http.Request) {
	if h.msftProvider == nil {
		jsonError(w, "microsoft_oauth_not_configured", http.StatusServiceUnavailable)
		return
	}
	state, err := generateState()
	if err != nil {
		h.log.Error("failed to generate oauth state", "error", err)
		jsonError(w, "internal_error", http.StatusInternalServerError)
		return
	}
	if err := h.redis.SetEx(r.Context(), stateKey(state), "microsoft", 10*time.Minute).Err(); err != nil {
		h.log.Error("failed to store oauth state in redis", "error", err)
		jsonError(w, "internal_error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, h.msftProvider.AuthURL(state, h.cfg.MicrosoftRedirectURI), http.StatusFound)
}

// HandleMicrosoftCallback validates state, exchanges the code, fetches user info,
// upserts the user + email account row, stores encrypted tokens, and fires onSuccess.
func (h *Handler) HandleMicrosoftCallback(w http.ResponseWriter, r *http.Request) {
	if h.msftProvider == nil {
		jsonError(w, "microsoft_oauth_not_configured", http.StatusServiceUnavailable)
		return
	}
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.log.Warn("microsoft oauth denied by user", "error", errParam)
		jsonError(w, "oauth_denied", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		jsonError(w, "missing_code_or_state", http.StatusBadRequest)
		return
	}

	if err := h.validateAndConsumeState(r.Context(), state, "microsoft"); err != nil {
		h.log.Warn("microsoft oauth invalid state", "error", err)
		jsonError(w, "invalid_state", http.StatusBadRequest)
		return
	}

	pair, err := h.msftProvider.Exchange(r.Context(), code, h.cfg.MicrosoftRedirectURI)
	if err != nil {
		h.log.Error("microsoft token exchange failed", "error", err)
		jsonError(w, "token_exchange_failed", http.StatusBadGateway)
		return
	}

	if pair.AccessTokenPlaintext == nil {
		h.log.Error("microsoft exchange returned no access token plaintext")
		jsonError(w, "token_exchange_failed", http.StatusBadGateway)
		return
	}

	email, name, err := h.fetchMicrosoftUserEmail(r.Context(), *pair.AccessTokenPlaintext)
	if err != nil {
		h.log.Error("failed to fetch microsoft user info", "error", err)
		jsonError(w, "userinfo_failed", http.StatusBadGateway)
		return
	}

	userID, err := h.upsertUser(r.Context(), email, name)
	if err != nil {
		h.log.Error("failed to upsert user", "email", email, "error", err)
		jsonError(w, "internal_error", http.StatusInternalServerError)
		return
	}

	accountID, err := h.tokenStore.UpsertAccountWithTokens(r.Context(), userID, string(ProviderOutlook), email, pair)
	if err != nil {
		h.log.Error("failed to save microsoft tokens", "user_id", userID, "error", err)
		jsonError(w, "token_storage_failed", http.StatusInternalServerError)
		return
	}

	h.log.Info("microsoft oauth complete", "user_id", userID, "account_id", accountID, "email", email)
	h.fireOnSuccess(r.Context(), userID)
	http.Redirect(w, r, "/", http.StatusFound)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (h *Handler) upsertUser(ctx context.Context, email, name string) (uuid.UUID, error) {
	newID := uuid.New()
	var userID uuid.UUID
	err := h.db.QueryRowContext(ctx, `
		INSERT INTO users (id, email, name, encryption_key_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (email) DO UPDATE SET name = EXCLUDED.name
		RETURNING id
	`, newID, email, name, h.cfg.KMSKeyID).Scan(&userID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("upsert user %s: %w", email, err)
	}
	return userID, nil
}

type googleUserInfo struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

func (h *Handler) fetchGoogleUserEmail(ctx context.Context, accessToken string) (email, name string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleUserInfoURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("create google userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("google userinfo request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("google userinfo returned %d: %s", resp.StatusCode, body)
	}

	var info googleUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", "", fmt.Errorf("decode google userinfo: %w", err)
	}
	if info.Email == "" {
		return "", "", fmt.Errorf("google userinfo returned empty email")
	}
	return info.Email, info.Name, nil
}

type msUserInfo struct {
	DisplayName       string `json:"displayName"`
	Mail              string `json:"mail"`
	UserPrincipalName string `json:"userPrincipalName"`
}

func (h *Handler) fetchMicrosoftUserEmail(ctx context.Context, accessToken string) (email, name string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, msGraphBaseURL+"/me", nil)
	if err != nil {
		return "", "", fmt.Errorf("create microsoft userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("microsoft userinfo request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("microsoft graph /me returned %d: %s", resp.StatusCode, body)
	}

	var info msUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", "", fmt.Errorf("decode microsoft userinfo: %w", err)
	}
	// mail can be nil for some AAD accounts; fall back to userPrincipalName
	if info.Mail != "" {
		return info.Mail, info.DisplayName, nil
	}
	if info.UserPrincipalName != "" {
		return info.UserPrincipalName, info.DisplayName, nil
	}
	return "", "", fmt.Errorf("microsoft graph /me returned no email address")
}

func (h *Handler) validateAndConsumeState(ctx context.Context, state, expectedProvider string) error {
	stored, err := h.redis.GetDel(ctx, stateKey(state)).Result()
	if err == goredis.Nil {
		return fmt.Errorf("state not found or expired")
	}
	if err != nil {
		return fmt.Errorf("redis error validating state: %w", err)
	}
	if stored != expectedProvider {
		return fmt.Errorf("state provider mismatch: got %q want %q", stored, expectedProvider)
	}
	return nil
}

func (h *Handler) fireOnSuccess(ctx context.Context, userID uuid.UUID) {
	if h.onSuccess == nil {
		return
	}
	if err := h.onSuccess(ctx, userID); err != nil {
		h.log.Error("post-auth callback failed", "user_id", userID, "error", err)
	}
}

func stateKey(state string) string {
	return "oauth:state:" + state
}

func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func jsonError(w http.ResponseWriter, errCode string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q}`+"\n", errCode)
}
