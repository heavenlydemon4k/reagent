// Package batch provides HTTP handlers for the batch management API.
package batch

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/decisionstack/sync/internal/auth"
	"github.com/decisionstack/sync/internal/logger"
	"github.com/decisionstack/sync/internal/models"
	"github.com/go-chi/chi/v5"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

// Handler holds the batch HTTP handlers and their dependencies.
type Handler struct {
	qm *QueueManager
}

// NewHandler creates a batch handler mounted with the given DB and Redis.
func NewHandler(db *sqlx.DB, redis redis.UniversalClient) *Handler {
	return &Handler{
		qm: NewQueueManager(db, redis),
	}
}

// Router returns a chi.Router with all batch endpoints registered.
// Mount under /api/v1/batch (auth middleware applied upstream).
func (h *Handler) Router() chi.Router {
	r := chi.NewRouter()
	r.Get("/", h.handleGetBatch)
	r.Get("/next", h.handleGetNextCard)
	r.Get("/count", h.handleGetCount)
	r.Post("/dismiss", h.handleDismiss)
	return r
}

// ---------------------------------------------------------------------------
// GET /batch
// ---------------------------------------------------------------------------

// handleGetBatch returns the current pending batch for the authenticated user.
//
// Query params:
//   - limit (int, optional): max cards to return, default 20, max 100
//
// Response: { size, estimated_clear_time_minutes, cards[] }
func (h *Handler) handleGetBatch(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.MustGetUserID(r.Context())
	if err != nil {
		sendError(w, http.StatusUnauthorized, models.ErrCodeAuthExpired, "Authentication required")
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 20
	if limitStr != "" {
		if parsed, perr := strconv.Atoi(limitStr); perr == nil && parsed > 0 {
			limit = parsed
		}
	}

	batch, err := h.qm.GetBatch(r.Context(), userID, limit)
	if err != nil {
		logger.Error("failed to get batch", "error", err, "user_id", userID)
		sendError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve batch")
		return
	}

	sendJSON(w, http.StatusOK, batch)
}

// ---------------------------------------------------------------------------
// GET /batch/next
// ---------------------------------------------------------------------------

// handleGetNextCard returns the single highest-urgency pending card.
//
// Response: single DecisionCard or 404 if queue is empty.
func (h *Handler) handleGetNextCard(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.MustGetUserID(r.Context())
	if err != nil {
		sendError(w, http.StatusUnauthorized, models.ErrCodeAuthExpired, "Authentication required")
		return
	}

	card, err := h.qm.GetNextCard(r.Context(), userID)
	if err != nil {
		if syncErr, ok := err.(*models.SyncError); ok {
			sendError(w, http.StatusNotFound, syncErr.Code, syncErr.Message)
			return
		}
		logger.Error("failed to get next card", "error", err, "user_id", userID)
		sendError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve next card")
		return
	}

	sendJSON(w, http.StatusOK, card)
}

// ---------------------------------------------------------------------------
// GET /batch/count
// ---------------------------------------------------------------------------

// handleGetCount returns a quick summary of pending and urgent card counts.
//
// Response: { pending_count, urgent_count }
func (h *Handler) handleGetCount(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.MustGetUserID(r.Context())
	if err != nil {
		sendError(w, http.StatusUnauthorized, models.ErrCodeAuthExpired, "Authentication required")
		return
	}

	pending, urgent, err := h.qm.GetCounts(r.Context(), userID)
	if err != nil {
		logger.Error("failed to get counts", "error", err, "user_id", userID)
		sendError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve counts")
		return
	}

	sendJSON(w, http.StatusOK, map[string]int{
		"pending_count": pending,
		"urgent_count":  urgent,
	})
}

// ---------------------------------------------------------------------------
// POST /batch/dismiss
// ---------------------------------------------------------------------------

type dismissRequest struct {
	DismissedAt *time.Time `json:"dismissed_at"`
}

// handleDismiss records a batch notification dismissal for analytics.
// The cards themselves are NOT affected — only the notification.
//
// Body: { dismissed_at: "2024-01-15T10:30:00Z" }
// Response: 204 No Content on success.
func (h *Handler) handleDismiss(w http.ResponseWriter, r *http.Request) {
	userID, err := auth.MustGetUserID(r.Context())
	if err != nil {
		sendError(w, http.StatusUnauthorized, models.ErrCodeAuthExpired, "Authentication required")
		return
	}

	var req dismissRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, http.StatusBadRequest, "invalid_request", "Invalid request body: expected { dismissed_at }")
		return
	}

	dismissedAt := time.Now().UTC()
	if req.DismissedAt != nil {
		dismissedAt = *req.DismissedAt
	}

	store := NewCardStore(h.qm.DB())
	if err := store.RecordDismissal(r.Context(), userID, dismissedAt); err != nil {
		logger.Error("failed to record dismissal", "error", err, "user_id", userID)
		sendError(w, http.StatusInternalServerError, "internal_error", "Failed to record dismissal")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// RESPONSE HELPERS
// ---------------------------------------------------------------------------

func sendJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		logger.Error("failed to encode response", "error", err)
	}
}

func sendError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(models.SyncError{
		Code:    code,
		Message: message,
		Retry:   status >= 500,
	}); err != nil {
		logger.Error("failed to encode error response", "error", err)
	}
}
