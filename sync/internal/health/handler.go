// Package health provides HTTP health and readiness check endpoints.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/decisionstack/sync/internal/db"
	"github.com/decisionstack/sync/internal/logger"
	"github.com/decisionstack/sync/internal/redis"
)

// HealthHandler provides health check HTTP handlers.
type HealthHandler struct {
	db      *db.DB
	redis   *redis.Redis
	startAt time.Time
}

// NewHealthHandler creates a new health handler.
func NewHealthHandler(dbConn *db.DB, redisConn *redis.Redis) *HealthHandler {
	return &HealthHandler{
		db:      dbConn,
		redis:   redisConn,
		startAt: time.Now(),
	}
}

// RegisterRoutes registers health check routes (no auth required).
func (h *HealthHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", h.HealthCheck)
	mux.HandleFunc("/ready", h.ReadinessCheck)
}

// HealthCheck returns a basic health status.
func (h *HealthHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"uptime":    time.Since(h.startAt).String(),
		"service":   "sync",
	})
}

// ReadinessCheck returns readiness status including dependency health.
func (h *HealthHandler) ReadinessCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	status := "ready"
	code := http.StatusOK
	checks := map[string]string{}

	// Check database
	if h.db != nil {
		if err := h.db.Health(ctx); err != nil {
			checks["database"] = "unavailable: " + err.Error()
			status = "not_ready"
			code = http.StatusServiceUnavailable
			logger.Error("readiness check: database unavailable", "error", err)
		} else {
			checks["database"] = "ok"
		}
	} else {
		checks["database"] = "not_configured"
	}

	// Check Redis
	if h.redis != nil {
		if err := h.redis.Health(ctx); err != nil {
			checks["redis"] = "unavailable: " + err.Error()
			status = "not_ready"
			code = http.StatusServiceUnavailable
			logger.Error("readiness check: redis unavailable", "error", err)
		} else {
			checks["redis"] = "ok"
		}
	} else {
		checks["redis"] = "not_configured"
	}

	writeJSON(w, code, map[string]any{
		"status": status,
		"checks": checks,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		logger.Error("failed to encode health response", "error", err)
	}
}
