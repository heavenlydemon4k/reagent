package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/decisionstack/ingestion/internal/config"
	"github.com/decisionstack/ingestion/internal/webhook"
)

// Dependencies holds all service dependencies for injection into handlers.
type Dependencies struct {
	WebhookHandler *webhook.WebhookHandler
	Log            *slog.Logger
}

// NewRouter creates a new Chi router with all routes and middleware configured.
func NewRouter(cfg *config.Config, deps *Dependencies) *chi.Mux {
	r := chi.NewRouter()

	// Global middleware
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(loggingMiddleware(deps.Log))
	r.Use(recoveryMiddleware(deps.Log))

	// Health check
	r.Get("/health", handleHealth(deps.Log))

	// Webhook routes — POST only
	r.Post("/webhooks/gmail", deps.WebhookHandler.HandleGmail)
	r.Post("/webhooks/outlook", deps.WebhookHandler.HandleOutlook)

	// OAuth routes placeholder — mounted at /auth/*
	// (implemented by the OAuth track)
	r.Route("/auth", func(r chi.Router) {
		// Placeholder for OAuth handler routes
		r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})

	// API v1 routes placeholder
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})

	// Unused but keep cfg reference valid
	_ = cfg.ServerPort

	return r
}

// loggingMiddleware logs each HTTP request with timing.
func loggingMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				latency := time.Since(start)
				log.Info("http request",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Int("status", ww.Status()),
					slog.Int("bytes", ww.BytesWritten()),
					slog.Duration("latency", latency),
					slog.String("request_id", middleware.GetReqID(r.Context())),
				)
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

// recoveryMiddleware recovers from panics and returns 500.
func recoveryMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic recovered",
						slog.Any("recover", rec),
						slog.String("stack", string(debug.Stack())),
						slog.String("path", r.URL.Path),
						slog.String("method", r.Method),
					)

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					json.NewEncoder(w).Encode(map[string]string{
						"error": "internal server error",
					})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// handleHealth is the health check handler.
func handleHealth(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status":  "healthy",
			"time":    time.Now().UTC().Format(time.RFC3339),
			"version": os.Getenv("APP_VERSION"),
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Warn("failed to encode health response", slog.String("error", err.Error()))
		}
	}
}
