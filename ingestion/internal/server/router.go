// Package server wires all HTTP handlers into a single chi router.
//
// Phase 0 scope: this file exists so that the server package compiles and
// go test ./... passes. Full handler wiring happens in Phase 1.
package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/decisionstack/ingestion/internal/config"
	"github.com/decisionstack/ingestion/internal/health"
	appmw "github.com/decisionstack/ingestion/internal/middleware"
	"github.com/decisionstack/ingestion/internal/nats"
	"github.com/decisionstack/ingestion/internal/oauth"
	"github.com/decisionstack/ingestion/internal/webhook"
)

// Dependencies bundles all handler-level dependencies required by the router.
// Each field maps directly to a package that provides an HTTP handler or is
// used by a handler.
type Dependencies struct {
	Log            *slog.Logger
	Config         *config.Config
	WebhookHandler *webhook.WebhookHandler
	OAuthHandler   *oauth.Handler
	HealthHandler  *health.Handler
	NATSPublisher  nats.Publisher
	SendHandler    *SendHandler
}

// NewRouter builds and returns the chi router with all middleware and routes
// mounted. It does not start the server; the caller owns that lifecycle.
func NewRouter(cfg *config.Config, deps *Dependencies) *chi.Mux {
	r := chi.NewRouter()

	// ── Global middleware (applied to every request) ────────────────────────
	r.Use(chimiddleware.RealIP)
	r.Use(appmw.RequestID)
	r.Use(appmw.Logging)
	r.Use(appmw.Recovery)
	r.Use(chimiddleware.Timeout(30 * time.Second))
	r.Use(appmw.SecurityHeaders)

	// ── Health ──────────────────────────────────────────────────────────────
	if deps.HealthHandler != nil {
		r.Get("/health", deps.HealthHandler.ServeHTTP)
	} else {
		r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"status":"ok","service":"ingestion"}`+"\n")
		})
	}

	// ── Webhooks (Gmail Pub/Sub, Outlook Graph) ──────────────────────────────
	if deps.WebhookHandler != nil {
		r.Mount("/webhooks", deps.WebhookHandler.Routes())
	}

	// ── OAuth callbacks ──────────────────────────────────────────────────────
	// Full OAuth flow wired in Phase 1. Stub routes so the service starts and
	// can respond to probe traffic on /auth/*.
	if deps.OAuthHandler != nil {
		r.Mount("/auth", deps.OAuthHandler.Routes())
	}

	// ── Outbound send (called by Intelligence service) ───────────────────────
	if deps.SendHandler != nil {
		r.Post("/api/v1/send", deps.SendHandler.HandleSend)
	}

	// ── 404 catch-all ────────────────────────────────────────────────────────
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"error":"not found","path":"%s"}`+"\n", r.URL.Path)
	})

	return r
}
