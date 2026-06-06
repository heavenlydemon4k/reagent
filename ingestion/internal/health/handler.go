// Package health provides the /health HTTP endpoint for the Ingestion Mesh.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/decisionstack/ingestion/internal/logger"
)

// Checker is the interface for dependency health checks.
type Checker interface {
	Ping(ctx context.Context) error
}

// NATSChecker is the interface for NATS health checks.
type NATSChecker interface {
	HealthCheck() error
}

// Handler handles health check requests.
type Handler struct {
	version string
	db      Checker
	redis   Checker
	nats    NATSChecker
}

// Response is the health check response.
type Response struct {
	Status    string            `json:"status"`
	Service   string            `json:"service"`
	Version   string            `json:"version"`
	Timestamp time.Time         `json:"timestamp"`
	Checks    map[string]string `json:"checks,omitempty"`
}

// NewHandler creates a new health handler.
func NewHandler(version string, db Checker, redisClient Checker, natsClient NATSChecker) *Handler {
	return &Handler{
		version: version,
		db:      db,
		redis:   redisClient,
		nats:    natsClient,
	}
}

// ServeHTTP handles GET /health requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	resp := Response{
		Status:    "ok",
		Service:   "ingestion",
		Version:   h.version,
		Timestamp: time.Now().UTC(),
		Checks:    make(map[string]string),
	}

	allHealthy := true

	// Check PostgreSQL
	if h.db != nil {
		if err := h.db.Ping(ctx); err != nil {
			resp.Checks["postgres"] = "unhealthy: " + err.Error()
			allHealthy = false
		} else {
			resp.Checks["postgres"] = "ok"
		}
	}

	// Check Redis
	if h.redis != nil {
		if err := h.redis.Ping(ctx); err != nil {
			resp.Checks["redis"] = "unhealthy: " + err.Error()
			allHealthy = false
		} else {
			resp.Checks["redis"] = "ok"
		}
	}

	// Check NATS
	if h.nats != nil {
		if err := h.nats.HealthCheck(); err != nil {
			resp.Checks["nats"] = "unhealthy: " + err.Error()
			allHealthy = false
		} else {
			resp.Checks["nats"] = "ok"
		}
	}

	if !allHealthy {
		resp.Status = "degraded"
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		logger.Error(ctx, "health check response encoding failed", "error", err)
	}
}
