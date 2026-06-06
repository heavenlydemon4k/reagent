package auth

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/decisionstack/sync/internal/models"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Handler — Auth HTTP handlers
// ---------------------------------------------------------------------------

// Handler provides HTTP handlers for the auth subsystem.
type Handler struct {
	tm           *TokenManager
	deviceMgr    *DeviceManager
	hmacSecret   []byte // used for forward-auth user resolution
}

// NewHandler creates a Handler with the given dependencies.
func NewHandler(tm *TokenManager, deviceMgr *DeviceManager) *Handler {
	return &Handler{
		tm:         tm,
		deviceMgr:  deviceMgr,
		hmacSecret: tm.secret,
	}
}

// ---------------------------------------------------------------------------
// DTOs
// ---------------------------------------------------------------------------

// deviceRegisterRequest is POST /auth/device body.
type deviceRegisterRequest struct {
	DeviceID   string  `json:"device_id"`
	DeviceType string  `json:"device_type"`
	DeviceName string  `json:"device_name"`
	FCMToken   *string `json:"fcm_token,omitempty"`
	APNSToken  *string `json:"apns_token,omitempty"`
}

func (r deviceRegisterRequest) Validate() error {
	if r.DeviceID == "" {
		return &validationErr{field: "device_id", msg: "device_id is required"}
	}
	if r.DeviceType != "ios" && r.DeviceType != "android" {
		return &validationErr{field: "device_type", msg: "device_type must be 'ios' or 'android'"}
	}
	if r.DeviceName == "" {
		return &validationErr{field: "device_name", msg: "device_name is required"}
	}
	return nil
}

// refreshRequest is POST /auth/refresh body.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
	DeviceID     string `json:"device_id"`
}

func (r refreshRequest) Validate() error {
	if r.RefreshToken == "" {
		return &validationErr{field: "refresh_token", msg: "refresh_token is required"}
	}
	if r.DeviceID == "" {
		return &validationErr{field: "device_id", msg: "device_id is required"}
	}
	return nil
}

// revokeRequest is POST /auth/revoke body.
type revokeRequest struct {
	DeviceID string `json:"device_id"`
}

func (r revokeRequest) Validate() error {
	if r.DeviceID == "" {
		return &validationErr{field: "device_id", msg: "device_id is required"}
	}
	return nil
}

// validationErr is an input-validation error.
type validationErr struct {
	field string
	msg   string
}

func (e *validationErr) Error() string { return e.msg }

// ---------------------------------------------------------------------------
// POST /auth/device — Register new device
// ---------------------------------------------------------------------------

// RegisterDevice handles POST /auth/device.
//
// Body: {device_id, device_type ("ios"|"android"), device_name, fcm_token?, apns_token?}
// Creates (or updates) the device session, returns JWT access token + refresh token.
func (h *Handler) RegisterDevice(w http.ResponseWriter, r *http.Request) {
	var req deviceRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "Request body is not valid JSON")
		return
	}

	if err := req.Validate(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	// Resolve user.  In a real deployment this endpoint sits behind an
	// authentication gateway (e.g., Firebase Auth, custom SSO) that injects
	// the authenticated user ID via a trusted header.  For integration tests
	// we fall back to a header-provided UUID so the flow can be exercised
	// end-to-end without a full identity provider.
	userID := resolveUserID(r)
	if userID == uuid.Nil {
		writeJSONError(w, http.StatusUnauthorized, "auth_required", "User authentication required")
		return
	}

	ctx := r.Context()

	// Upsert device session.
	session := &models.DeviceSession{
		ID:         uuid.New(),
		UserID:     userID,
		DeviceID:   req.DeviceID,
		DeviceType: req.DeviceType,
		DeviceName: req.DeviceName,
		FCMToken:   req.FCMToken,
		APNSToken:  req.APNSToken,
	}
	if err := h.deviceMgr.Register(ctx, session); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "server_error", "Failed to register device")
		return
	}

	// Generate tokens.
	accessToken, err := h.tm.GenerateAccessToken(userID, req.DeviceID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "token_error", "Failed to generate access token")
		return
	}

	refreshToken, err := h.tm.GenerateRefreshToken(userID, req.DeviceID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "token_error", "Failed to generate refresh token")
		return
	}

	// Store hashed refresh token.
	refreshHash := HashRefreshToken(refreshToken)
	if err := h.deviceMgr.Store().StoreRefreshToken(ctx, userID, req.DeviceID, refreshHash); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "server_error", "Failed to store refresh token")
		return
	}

	resp := models.TokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().UTC().Add(h.tm.accessTTL),
	}
	writeJSONObj(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// POST /auth/refresh — Refresh access token
// ---------------------------------------------------------------------------

// RefreshToken handles POST /auth/refresh.
//
// Body: {refresh_token, device_id}
// Validates the refresh token (including DB hash check), issues a new
// access token + a rotated refresh token.
func (h *Handler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "Request body is not valid JSON")
		return
	}

	if err := req.Validate(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	// Validate the JWT envelope of the refresh token.
	userID, deviceID, err := h.tm.ValidateRefreshToken(req.RefreshToken)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "auth_invalid", "Invalid refresh token")
		return
	}

	// Verify the device ID in the token matches the request.
	if deviceID != req.DeviceID {
		writeJSONError(w, http.StatusUnauthorized, "auth_mismatch", "Device ID does not match refresh token")
		return
	}

	ctx := r.Context()

	// Verify the opaque portion against the hashed value in the database.
	opaque := ExtractRefreshTokenSecret(req.RefreshToken)
	storedHash, err := h.deviceMgr.Store().GetRefreshToken(ctx, userID, deviceID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "server_error", "Failed to verify refresh token")
		return
	}
	if storedHash == "" {
		writeJSONError(w, http.StatusUnauthorized, "auth_revoked", "Refresh token has been revoked")
		return
	}

	// Compare hash of the full composite refresh token against stored hash.
	fullHash := HashRefreshToken(req.RefreshToken)
	if fullHash != storedHash {
		// For backward compatibility: the DB may contain the hash of just
		// the opaque portion. Try that too.
		opaqueHash := HashRefreshToken(opaque)
		if opaqueHash != storedHash {
			writeJSONError(w, http.StatusUnauthorized, "auth_invalid", "Refresh token does not match stored value")
			return
		}
	}

	// Ensure the device session still exists.
	existing, err := h.deviceMgr.GetByDeviceID(ctx, userID, deviceID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "server_error", "Failed to look up device session")
		return
	}
	if existing == nil {
		writeJSONError(w, http.StatusUnauthorized, "auth_revoked", "Device session no longer exists")
		return
	}

	// Generate new token pair (refresh rotation).
	newAccess, err := h.tm.GenerateAccessToken(userID, deviceID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "token_error", "Failed to generate access token")
		return
	}
	newRefresh, err := h.tm.GenerateRefreshToken(userID, deviceID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "token_error", "Failed to generate refresh token")
		return
	}

	// Store the new refresh token hash, replacing the old one.
	newHash := HashRefreshToken(newRefresh)
	if err := h.deviceMgr.Store().StoreRefreshToken(ctx, userID, deviceID, newHash); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "server_error", "Failed to store new refresh token")
		return
	}

	// Bump last_active.
	_ = h.deviceMgr.UpdateLastActive(ctx, userID, deviceID)

	resp := models.TokenResponse{
		AccessToken:  newAccess,
		RefreshToken: newRefresh,
		ExpiresAt:    time.Now().UTC().Add(h.tm.accessTTL),
	}
	writeJSONObj(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// POST /auth/revoke — Revoke device session
// ---------------------------------------------------------------------------

// RevokeSession handles POST /auth/revoke.
//
// Body: {device_id}
// Deletes the device session and invalidates all tokens for that device.
func (h *Handler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	if userID == uuid.Nil {
		writeJSONError(w, http.StatusUnauthorized, "auth_required", "Authentication required")
		return
	}

	var req revokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "Request body is not valid JSON")
		return
	}

	if err := req.Validate(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	ctx := r.Context()

	// Verify the session belongs to the requesting user.
	existing, err := h.deviceMgr.GetByDeviceID(ctx, userID, req.DeviceID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "server_error", "Failed to look up session")
		return
	}
	if existing == nil {
		writeJSONError(w, http.StatusNotFound, "session_not_found", "Device session not found")
		return
	}

	if err := h.deviceMgr.Revoke(ctx, userID, req.DeviceID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "server_error", "Failed to revoke session")
		return
	}

	writeJSONObj(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// ---------------------------------------------------------------------------
// GET /auth/sessions — List active sessions
// ---------------------------------------------------------------------------

// ListSessions handles GET /auth/sessions.
// Returns all device sessions for the authenticated user with last active time.
func (h *Handler) ListSessions(w http.ResponseWriter, r *http.Request) {
	userID := UserIDFromContext(r.Context())
	if userID == uuid.Nil {
		writeJSONError(w, http.StatusUnauthorized, "auth_required", "Authentication required")
		return
	}

	sessions, err := h.deviceMgr.ListByUser(r.Context(), userID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "server_error", "Failed to list sessions")
		return
	}

	writeJSONObj(w, http.StatusOK, sessions)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// writeJSONObj marshals v to JSON and writes it with the given status code.
func writeJSONObj(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// resolveUserID extracts the authenticated user ID from the request.
// In production this reads a trusted header set by the reverse proxy / auth
// gateway.  For local testing it falls back to an X-Test-User-ID header.
func resolveUserID(r *http.Request) uuid.UUID {
	// Production: trusted header from the API gateway / load balancer.
	if v := r.Header.Get("X-Authenticated-User-ID"); v != "" {
		if uid, err := uuid.Parse(v); err == nil {
			return uid
		}
	}
	// Test fallback — only honoured when X-Test-Auth: enabled is present.
	if r.Header.Get("X-Test-Auth") == "enabled" {
		if v := r.Header.Get("X-Test-User-ID"); v != "" {
			if uid, err := uuid.Parse(v); err == nil {
				return uid
			}
		}
	}
	return uuid.Nil
}

// ---------------------------------------------------------------------------
// Router wiring helper
// ---------------------------------------------------------------------------

// MountAuthRoutes registers auth routes on the provided Chi router.
func MountAuthRoutes(r chi.Router, h *Handler) {
	AuthRoutes(r, h)
}
