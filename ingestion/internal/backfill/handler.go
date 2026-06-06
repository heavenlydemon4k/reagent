package backfill

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// StatusHandler provides the HTTP endpoint for backfill progress monitoring.
// It is mounted into the ingestion server at GET /api/v1/backfill/status.
type StatusHandler struct {
	redis *redis.Client
	log   *slog.Logger
}

// NewStatusHandler creates a new backfill status HTTP handler.
func NewStatusHandler(redisClient *redis.Client, log *slog.Logger) *StatusHandler {
	return &StatusHandler{
		redis: redisClient,
		log:   log.With("component", "backfill_status_handler"),
	}
}

// ServeHTTP handles GET /api/v1/backfill/status?user_id={uuid}.
// It returns the current backfill progress for the given user.
func (h *StatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Extract user_id from query params
	userIDStr := r.URL.Query().Get("user_id")
	if userIDStr == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "user_id query parameter is required",
		})
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid user_id format",
		})
		return
	}

	// Use scheduler to read progress from Redis
	sched := NewScheduler(h.redis, h.log)
	ctx := r.Context()

	snap, err := sched.GetProgress(ctx, userID)
	if err != nil {
		h.log.Warn("no backfill progress found", "user_id", userID, "error", err)
		// Return a friendly response indicating no active backfill
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":           "not_found",
			"progress":         0,
			"emails_found":     0,
			"emails_processed": 0,
			"message":          "No backfill job found for this user",
		})
		return
	}

	// Build response matching the expected API contract
	resp := map[string]interface{}{
		"status":           snap.Status,
		"progress":         snap.Progress,
		"emails_found":     snap.EmailsFound,
		"emails_processed": snap.EmailsProcessed,
		"emails_skipped":   snap.EmailsSkipped,
		"emails_failed":    snap.EmailsFailed,
		"retry_count":      snap.RetryCount,
	}

	if snap.LastError != "" {
		resp["last_error"] = snap.LastError
	}
	if !snap.StartedAt.IsZero() {
		resp["started_at"] = snap.StartedAt.Format("2006-01-02T15:04:05Z")
	}
	if snap.CompletedAt != nil {
		resp["completed_at"] = snap.CompletedAt.Format("2006-01-02T15:04:05Z")
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.log.Error("failed to encode response", "error", err)
	}
}
