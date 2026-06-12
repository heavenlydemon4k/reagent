package sync

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/decisionstack/sync/internal/auth"
	"github.com/decisionstack/sync/internal/logger"
	"github.com/decisionstack/sync/internal/models"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ---------------------------------------------------------------------------
// SyncHandler — HTTP handler for POST /sync
// ---------------------------------------------------------------------------

// SyncHandler handles the sync endpoint: POST /api/v1/sync
//
// Request body:  models.SyncRequest  {device_id, last_sync_version, local_changes}
// Response body: models.SyncResponse {server_version, accepted_changes, rejected_changes, new_cards, updated_cards, removed_cards}
//
// Authentication: Bearer JWT (injected by auth middleware into context).
// The handler is idempotent: the same request body produces the same response.
type SyncHandler struct {
	engine *SyncEngine
	log    *slog.Logger
	db     *sqlx.DB
}

// NewSyncHandler creates a new SyncHandler with the given database.
func NewSyncHandler(db *sqlx.DB) *SyncHandler {
	return &SyncHandler{
		engine: NewSyncEngine(db, logger.L()),
		log:    logger.L(),
		db:     db,
	}
}

// ---------------------------------------------------------------------------
// Router
// ---------------------------------------------------------------------------

// Router returns a Chi router with the sync endpoint mounted.
func (h *SyncHandler) Router() chi.Router {
	r := chi.NewRouter()
	r.Post("/", h.HandleSync)
	return r
}

// ---------------------------------------------------------------------------
// POST /sync — Main sync endpoint
// ---------------------------------------------------------------------------

// HandleSync processes a sync request from a client.
//
// Flow:
//   1. Extract userID and deviceID from context (JWT).
//   2. Parse and validate the SyncRequest body.
//   3. Call SyncEngine.Process to execute the 3-phase CRDT merge.
//   4. Return SyncResponse as JSON.
//
// Error responses:
//   400 — invalid request body
//   401 — authentication required
//   429 — rate limited (sync too frequently)
//   500 — internal server error
func (h *SyncHandler) HandleSync(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// --- Step 1: Extract user ID from context (set by JWT middleware) ---
	userID := auth.UserIDFromContext(ctx)
	if userID == uuid.Nil {
		h.writeError(w, http.StatusUnauthorized, models.ErrCodeAuthExpired, "Authentication required")
		return
	}

	// --- Step 2: Extract device ID from context ---
	deviceID := auth.DeviceIDFromContext(ctx)

	// --- Step 3: Parse request body ---
	var req models.SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.log.Warn("sync request: invalid JSON", "error", err, "user_id", userID)
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Request body is not valid JSON")
		return
	}
	defer r.Body.Close()

	// --- Step 4: Validate request ---
	if err := validateSyncRequest(&req); err != nil {
		h.log.Warn("sync request: validation failed", "error", err, "user_id", userID)
		h.writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	// If deviceID not in context, try from request body (for clients that
	// include it in the payload rather than the token).
	if deviceID == "" {
		deviceID = req.DeviceID
	}

	h.log.Debug("sync request received",
		"user_id", userID,
		"device_id", deviceID,
		"last_sync_version", req.LastSyncVersion,
		"local_changes", len(req.LocalChanges),
	)

	// --- Step 5: Execute sync engine ---
	resp, err := h.engine.Process(ctx, userID, deviceID, &req)
	if err != nil {
		h.log.Error("sync engine failed", "error", err, "user_id", userID)
		h.writeError(w, http.StatusInternalServerError, "sync_failed", "Sync processing failed")
		return
	}

	// --- Step 6: Write response ---
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.log.Error("failed to encode sync response", "error", err, "user_id", userID)
	}
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

// validateSyncRequest validates a sync request body.
func validateSyncRequest(req *models.SyncRequest) error {
	// Validate each local change.
	for i, change := range req.LocalChanges {
		if change.CardID == uuid.Nil {
			return fmt.Errorf("local_changes[%d]: card_id is required", i)
		}
		if change.Decision != nil {
			d := *change.Decision
			if d != "approve" && d != "edit" && d != "consult" {
				return fmt.Errorf("local_changes[%d]: decision must be 'approve', 'edit', or 'consult', got %q", i, d)
			}
		}
		if change.Version < 0 {
			return fmt.Errorf("local_changes[%d]: version cannot be negative", i)
		}
	}

	// Validate device_id format if present.
	if req.DeviceID != "" {
		if len(req.DeviceID) > 256 {
			return fmt.Errorf("device_id: must be at most 256 characters")
		}
		// device_id should not contain control characters
		for _, r := range req.DeviceID {
			if r < 32 {
				return fmt.Errorf("device_id: contains invalid characters")
			}
		}
	}

	// last_sync_version must be non-negative.
	if req.LastSyncVersion < 0 {
		return fmt.Errorf("last_sync_version: cannot be negative")
	}

	return nil
}

// ---------------------------------------------------------------------------
// JSON helpers
// ---------------------------------------------------------------------------

func (h *SyncHandler) writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(models.SyncError{
		Code:    code,
		Message: message,
		Retry:   status >= 500,
	})
}

// ---------------------------------------------------------------------------
// Rate-limit key helper
// ---------------------------------------------------------------------------

// syncRateLimitKey builds a rate-limit key for sync requests.
// Format: "sync:{user_id}:{device_id}" — limits per device to prevent
// sync storms from buggy clients.
func syncRateLimitKey(userID uuid.UUID, deviceID string) string {
	if deviceID == "" {
		deviceID = "unknown"
	}
	// Sanitise deviceID for use in a Redis key.
	safe := strings.ReplaceAll(deviceID, ":", "_")
	return fmt.Sprintf("sync:%s:%s", userID.String(), safe)
}

// SyncRateLimitWindow is the duration of the rate-limit window for sync.
const SyncRateLimitWindow = 5 * time.Second

// SyncRateLimitMaxRequests is the maximum sync requests per window.
const SyncRateLimitMaxRequests = 10
