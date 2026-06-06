// cmd/server is the HTTP server entry point for the Ingestion Mesh.
// It provides webhook endpoints, health checks, and OAuth callback handlers.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/decisionstack/ingestion/internal/backfill"
	"github.com/decisionstack/ingestion/internal/config"
	"github.com/decisionstack/ingestion/internal/crypto"
	"github.com/decisionstack/ingestion/internal/db"
	"github.com/decisionstack/ingestion/internal/fetch"
	"github.com/decisionstack/ingestion/internal/health"
	"github.com/decisionstack/ingestion/internal/logger"
	natspkg "github.com/decisionstack/ingestion/internal/nats"
	"github.com/decisionstack/ingestion/internal/oauth"
	"github.com/decisionstack/ingestion/internal/redis"
	"github.com/decisionstack/ingestion/internal/middleware"
	"github.com/decisionstack/ingestion/internal/webhook"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Initialize logger
	logger.Init(cfg)
	log := logger.L().With("service", "server")
	ctx := context.Background()

	log.Info(ctx, "starting ingestion server", "version", cfg.AppVersion, "environment", cfg.Environment)

	// Initialize database
	database, err := db.New(cfg)
	if err != nil {
		log.Error(ctx, "failed to connect to database", "error", err)
		return fmt.Errorf("init database: %w", err)
	}
	defer database.Close()
	log.Info(ctx, "database connected")

	// Initialize Redis
	redisClient, err := redis.New(cfg)
	if err != nil {
		log.Error(ctx, "failed to connect to redis", "error", err)
		return fmt.Errorf("init redis: %w", err)
	}
	defer redisClient.Close()
	log.Info(ctx, "redis connected")

	// Initialize NATS publisher
	natsPublisher, err := natspkg.NewJetStreamPublisher(cfg.NATSURL)
	if err != nil {
		log.Error(ctx, "failed to connect to NATS", "error", err)
		return fmt.Errorf("init nats: %w", err)
	}
	defer natsPublisher.Close()
	log.Info(ctx, "nats connected")

	// Initialize KMS client for token encryption
	kmsClient, err := crypto.NewKMSClient(cfg)
	if err != nil {
		log.Error(ctx, "failed to initialize KMS client", "error", err)
		return fmt.Errorf("init kms: %w", err)
	}
	defer kmsClient.Close()

	// Initialize token crypto for OAuth token encryption/decryption
	tokenCrypto := crypto.NewTokenCrypto(kmsClient)

	// Initialize slog logger for webhook and fetch packages
	slogLogger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slogLevelFromString(cfg.LogLevel),
	}))

	// Initialize fetch enqueuer for webhook-driven fetch jobs
	fetchEnqueuer := fetch.NewEnqueuer(redisClient.Client(), natsPublisher, slogLogger)

	// Initialize handlers
	authHandler := oauth.NewHandler(cfg, database.Pool(), redisClient.Client(), tokenCrypto, natsPublisher)
	webhookHandler := webhook.NewHandler(cfg, redisClient.Client(), natsPublisher, fetchEnqueuer, slogLogger)

	// Build HTTP router
	r := chi.NewRouter()

	// Middleware stack (outer to inner)
	r.Use(middleware.Recovery)
	r.Use(middleware.RequestID)
	r.Use(middleware.Logging)
	r.Use(appmiddleware.SecurityHeaders)
	r.Use(appmiddleware.RateLimit(redisClient, 200, 1*time.Minute))

	// Health check
	healthHandler := health.NewHandler(cfg.AppVersion, database, redisClient, natsPublisher)
	r.Get("/health", healthHandler.ServeHTTP)

	// OAuth routes — wired from oauth handler
	r.Mount("/auth", authHandler.Routes())

	// Webhook routes — wired from webhook handler
	r.Mount("/webhooks", webhookHandler.Routes())

	// Backfill status endpoint — polled by client during onboarding
	backfillStatusHandler := backfill.NewStatusHandler(redisClient.Client(), slogLogger)

	// API routes
	r.Route("/api/v1", func(r chi.Router) {
		// Accounts management
		r.Route("/accounts", func(r chi.Router) {
			r.Get("/", stubHandler("accounts.list"))
			r.Post("/", stubHandler("accounts.create"))
			r.Get("/{id}", stubHandler("accounts.get"))
			r.Delete("/{id}", stubHandler("accounts.delete"))
		})
		// Ingestion jobs
		r.Route("/jobs", func(r chi.Router) {
			r.Get("/", stubHandler("jobs.list"))
			r.Post("/poll", stubHandler("jobs.trigger_poll"))
		})
		// Backfill progress — client polls this during onboarding
		r.Get("/backfill/status", backfillStatusHandler.ServeHTTP)
	})

	// Server configuration
	addr := fmt.Sprintf("%s:%s", cfg.ServerHost, cfg.ServerPort)
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Info(ctx, "server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error(ctx, "server listen error", "error", err)
		}
	}()

	<-stop
	log.Info(ctx, "shutdown signal received, gracefully shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error(ctx, "server shutdown error", "error", err)
		return err
	}

	log.Info(ctx, "server stopped gracefully")
	return nil
}

// stubHandler returns a simple stub handler for routes implemented by other tracks.
func stubHandler(operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		fmt.Fprintf(w, `{"status":"not_implemented","operation":"%s"}`, operation)
	}
}

// slogLevelFromString converts a string log level to slog.Level.
func slogLevelFromString(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
