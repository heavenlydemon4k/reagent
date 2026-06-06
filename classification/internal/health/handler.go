// Package health provides HTTP health, readiness, and liveness endpoints.
package health

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/decisionstack/classification/internal/config"
	"github.com/decisionstack/classification/internal/db"
	"github.com/decisionstack/classification/internal/logger"
	"github.com/decisionstack/classification/internal/redis"
	"github.com/go-chi/chi/v5"
	"github.com/nats-io/nats.go"
)

// Checker abstracts dependency health checks.
type Checker struct {
	dbPool   *db.Pool
	redis    *redis.Client
	natsConn *nats.Conn
	log      *logger.Logger
	build    BuildInfo
}

// BuildInfo contains build-time metadata.
type BuildInfo struct {
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	BuildTime  string `json:"build_time"`
	GoVersion  string `json:"go_version"`
}

// NewChecker creates a health checker.
func NewChecker(dbPool *db.Pool, redis *redis.Client, nc *nats.Conn, log *logger.Logger) *Checker {
	return &Checker{
		dbPool:   dbPool,
		redis:    redis,
		natsConn: nc,
		log:      log.WithComponent("health"),
		build: BuildInfo{
			Version:   "0.1.0",
			Commit:    config.GitRevision,
			BuildTime: config.BuildTime,
			GoVersion: "1.22",
		},
	}
}

// Routes mounts health endpoints on the given router.
func (c *Checker) Routes(r chi.Router) {
	r.Get("/health", c.handleHealth)
	r.Get("/ready", c.handleReady)
	r.Get("/live", c.handleLive)
}

func (c *Checker) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"build":     c.build,
		"services":  c.checkServices(r.Context()),
	}

	w.Header().Set("Content-Type", "application/json")
	if allHealthy(status["services"].(map[string]string)) {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		status["status"] = "unhealthy"
	}
	_ = json.NewEncoder(w).Encode(status)
}

func (c *Checker) handleReady(w http.ResponseWriter, r *http.Request) {
	// Readiness: can we serve traffic? (DB + Redis + NATS must be up)
	services := c.checkServices(r.Context())
	w.Header().Set("Content-Type", "application/json")
	if allHealthy(services) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":   "not_ready",
			"services": services,
		})
	}
}

func (c *Checker) handleLive(w http.ResponseWriter, r *http.Request) {
	// Liveness: is the process running?
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "alive"})
}

func (c *Checker) checkServices(ctx context.Context) map[string]string {
	results := make(map[string]string)

	if c.dbPool != nil {
		if err := c.dbPool.Health(ctx); err != nil {
			results["postgres"] = "unhealthy: " + err.Error()
		} else {
			results["postgres"] = "healthy"
		}
	} else {
		results["postgres"] = "not_configured"
	}

	if c.redis != nil {
		if err := c.redis.Health(ctx); err != nil {
			results["redis"] = "unhealthy: " + err.Error()
		} else {
			results["redis"] = "healthy"
		}
	} else {
		results["redis"] = "not_configured"
	}

	if c.natsConn != nil {
		if c.natsConn.IsConnected() {
			results["nats"] = "healthy"
		} else {
			results["nats"] = "disconnected"
		}
	} else {
		results["nats"] = "not_configured"
	}

	return results
}

func allHealthy(services map[string]string) bool {
	for _, v := range services {
		if v != "healthy" && v != "not_configured" {
			return false
		}
	}
	return true
}
