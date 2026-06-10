// cmd/server/main.go — HTTP + WebSocket server entry point for the Sync & State service.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/decisionstack/sync/internal/auth"
	"github.com/decisionstack/sync/internal/batch"
	"github.com/decisionstack/sync/internal/config"
	dbpkg "github.com/decisionstack/sync/internal/db"
	"github.com/decisionstack/sync/internal/decision"
	"github.com/decisionstack/sync/internal/health"
	"github.com/decisionstack/sync/internal/logger"
	appmiddleware "github.com/decisionstack/sync/internal/middleware"
	"github.com/decisionstack/sync/internal/models"
	natspkg "github.com/decisionstack/sync/internal/nats"
	redispkg "github.com/decisionstack/sync/internal/redis"
	natsgo "github.com/nats-io/nats.go"
	"github.com/decisionstack/sync/internal/sync"
	"github.com/decisionstack/sync/internal/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// No-op interface stubs for dependencies not yet fully implemented
// ---------------------------------------------------------------------------

// noOpMeshClient is a placeholder decision.IngestionMeshClient until the real
// ingestion-mesh integration is wired.
type noOpMeshClient struct{}

func (n *noOpMeshClient) SendEmail(ctx context.Context, draftID uuid.UUID, userID uuid.UUID, draftBody, subject string, inReplyTo *string, references []string) (time.Time, string, error) {
	return time.Time{}, "", fmt.Errorf("ingestion mesh not configured")
}

// noOpSpawnEngine is a placeholder websocket.SpawnEngine until the real
// intelligence-layer integration is wired.
type noOpSpawnEngine struct{}

func (n *noOpSpawnEngine) GenerateParagraph(ctx context.Context, cardID uuid.UUID, triggerWord string, cursorPosition int, existingDraft string) (<-chan websocket.ParagraphChunk, <-chan error) {
	chunks := make(chan websocket.ParagraphChunk)
	errCh := make(chan error, 1)
	errCh <- fmt.Errorf("spawn engine not configured")
	close(chunks)
	close(errCh)
	return chunks, errCh
}

func (n *noOpSpawnEngine) CancelGeneration(cardID uuid.UUID) {}

// noOpSessionStore is a placeholder websocket.SessionStore.
type noOpSessionStore struct{}

func (n *noOpSessionStore) GetDraft(ctx context.Context, cardID uuid.UUID) (*models.Draft, error) {
	return nil, fmt.Errorf("session store not configured")
}

func (n *noOpSessionStore) UpdateDraftBody(ctx context.Context, cardID uuid.UUID, userID uuid.UUID, body string) error {
	return fmt.Errorf("session store not configured")
}

func (n *noOpSessionStore) ApproveDraft(ctx context.Context, draftID uuid.UUID) error {
	return fmt.Errorf("session store not configured")
}

func (n *noOpSessionStore) CreateStagedRule(ctx context.Context, cardID uuid.UUID, userID uuid.UUID, delegationType string) (uuid.UUID, error) {
	return uuid.Nil, fmt.Errorf("session store not configured")
}

func (n *noOpSessionStore) UpdateCardState(ctx context.Context, cardID uuid.UUID, userID uuid.UUID, newState string) error {
	return fmt.Errorf("session store not configured")
}

func main() {
	// ============================================================================
	// LOAD CONFIG
	// ============================================================================
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// ============================================================================
	// INITIALIZE LOGGER
	// ============================================================================
	logger.Init(cfg.LogLevel, cfg.LogFormat)
	logger.Info("sync service starting", "environment", cfg.Environment, "port", cfg.ServerPort)

	// ============================================================================
	// INITIALIZE DATABASE
	// ============================================================================
	db, err := dbpkg.New(cfg)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// ============================================================================
	// INITIALIZE REDIS
	// ============================================================================
	redis, err := redispkg.New(cfg)
	if err != nil {
		logger.Error("failed to connect to redis", "error", err)
		os.Exit(1)
	}
	defer redis.Close()

	// ============================================================================
	// INITIALIZE AUTH
	// ============================================================================
	tokenManager := auth.NewTokenManager([]byte(cfg.JWTSecret), cfg.JWTAccessExpiry, cfg.JWTRefreshExpiry)
	jwtMiddleware := auth.JWTMiddleware(tokenManager)
	deviceManager := auth.NewDeviceManager(db.DB)
	authHandler := auth.NewHandler(tokenManager, deviceManager)

	// ============================================================================
	// INITIALIZE NATS CONSUMER
	// ============================================================================
	natsConsumer, err := natspkg.NewConsumer(cfg)
	if err != nil {
		logger.Warn("nats consumer not available", "error", err)
	} else {
		defer natsConsumer.Close()
	}

	// ============================================================================
	// INITIALIZE NATS PUBLISHER (for ApprovalFlow)
	// ============================================================================
	var natsPublisher decision.NatsPublisher
	nc, err := natsgo.Connect(cfg.NATSURL, natsgo.Name("sync-server"))
	if err != nil {
		logger.Warn("nats publisher not available", "error", err)
		natsPublisher = &decision.NoOpNatsPublisher{}
	} else {
		js, err := nc.JetStream()
		if err != nil {
			logger.Warn("nats jetstream not available", "error", err)
			natsPublisher = &decision.NoOpNatsPublisher{}
		} else {
			natsPublisher = natspkg.NewSyncNatsAdapter(js)
			defer nc.Close()
		}
	}

	// ============================================================================
	// INITIALIZE DECISION HANDLER
	// ============================================================================
	cardStore := decision.NewCardStore(db.DB)
	draftStore := decision.NewDraftStore(db.DB)
	approvalFlow := decision.NewApprovalFlow(draftStore, cardStore, natsPublisher, logger.L())
	draftingProxy := decision.NewDraftingProxy(decision.DraftingProxyConfig{
		IntelligenceURL: os.Getenv("INTELLIGENCE_URL"),
		Timeout:         30 * time.Second,
	})
	consultProxy := decision.NewConsultProxy(decision.ConsultProxyConfig{
		IntelligenceURL: os.Getenv("INTELLIGENCE_URL"),
		Timeout:         30 * time.Second,
	})
	processor := decision.NewDecisionProcessor(draftingProxy, consultProxy, approvalFlow, cardStore, draftStore, logger.L())
	meshClient := &noOpMeshClient{}
	decisionHandler := decision.NewHandler(processor, meshClient, logger.L())

	// ============================================================================
	// INITIALIZE BATCH HANDLER
	// ============================================================================
	batchHandler := batch.NewHandler(db.DB, redis.Client())

	// ============================================================================
	// INITIALIZE SYNC HANDLER
	// ============================================================================
	syncHandler := sync.NewSyncHandler(db.DB)

	// ============================================================================
	// INITIALIZE WEBSOCKET HUB & HANDLER
	// ============================================================================
	tokenValidator := auth.NewTokenValidator(cfg.JWTSecret)
	wsHub := websocket.NewHub(cfg, redis, tokenValidator)
	wsHandler := websocket.NewWSHandler(wsHub, tokenManager, cfg, &noOpSpawnEngine{}, &noOpSessionStore{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wsHub.Run(ctx)

	// ============================================================================
	// START NATS CONSUMER
	// ============================================================================
	if natsConsumer != nil {
		if err := natsConsumer.Subscribe(ctx); err != nil {
			logger.Warn("nats subscription failed", "error", err)
		}
	}

	// ============================================================================
	// SETUP ROUTER
	// ============================================================================
	r := chi.NewRouter()

	// Global middleware
	r.Use(middleware.RealIP)
	r.Use(appmiddleware.RequestID)
	r.Use(appmiddleware.Logging)
	r.Use(appmiddleware.Recovery)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(appmiddleware.SecurityHeaders)
	r.Use(appmiddleware.WithRateLimits(redis.Client(), appmiddleware.DefaultRateLimits()))

	// ============================================================================
	// PUBLIC ROUTES (no auth required)
	// ============================================================================

	// Health checks
	healthHandler := health.NewHealthHandler(db, redis)
	r.Mount("/health", http.HandlerFunc(healthHandler.HealthCheck))
	r.Mount("/ready", http.HandlerFunc(healthHandler.ReadinessCheck))

	// Auth routes (unauthenticated — device registration, refresh, revoke, list)
	auth.AuthRoutes(r, authHandler)

	// ============================================================================
	// AUTHENTICATED ROUTES — root level (not under /api/v1)
	// ============================================================================
	r.Group(func(r chi.Router) {
		r.Use(jwtMiddleware)

		// DECISION — 7 fully-implemented endpoints
		decisionHandler.Routes(r)
	})

	// WebSocket endpoint (auth handled via ?token= query param inside handler)
	wsHandler.Routes(r)

	// ============================================================================
	// AUTHENTICATED ROUTES — under /api/v1
	// ============================================================================
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(jwtMiddleware)

		// BATCH — mounted as sub-router
		r.Mount("/batch", batchHandler.Router())

		// SYNC — mounted as sub-router
		r.Mount("/sync", syncHandler.Router())

		// DEVICE SESSIONS
		r.Route("/devices", func(r chi.Router) {
			r.Post("/register", handleRegisterDevice(db))
			r.Delete("/{deviceID}", handleUnregisterDevice(db))
			r.Get("/", handleListDevices(db))
		})

		// NOTIFICATIONS
		r.Route("/notifications", func(r chi.Router) {
			r.Get("/", handleListNotifications(db))
			r.Post("/{notificationID}/read", handleMarkNotificationRead(db))
			r.Post("/preferences", handleUpdateNotificationPreferences(db))
		})

		// QUEUE
		r.Route("/queue", func(r chi.Router) {
			r.Get("/count", handleQueueCount(redis))
			r.Get("/version", handleQueueVersion(redis))
		})
	})

	// ============================================================================
	// START SERVER
	// ============================================================================
	srv := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		logger.Info("server listening", "addr", cfg.Addr())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-done
	logger.Info("server shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown error", "error", err)
	}

	cancel() // Cancel main context to stop background goroutines
	logger.Info("server stopped gracefully")
}

// ============================================================================
// DEVICE HANDLERS
// ============================================================================

func handleRegisterDevice(db *dbpkg.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sendError(w, http.StatusNotImplemented, "not_implemented", "Use POST /auth/device instead")
	}
}

func handleUnregisterDevice(db *dbpkg.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sendError(w, http.StatusNotImplemented, "not_implemented", "Use POST /auth/revoke instead")
	}
}

func handleListDevices(db *dbpkg.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sendError(w, http.StatusNotImplemented, "not_implemented", "Use GET /auth/sessions instead")
	}
}

// ============================================================================
// NOTIFICATION HANDLERS
// ============================================================================

func handleListNotifications(db *dbpkg.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := auth.MustGetUserID(r.Context())
		if err != nil {
			sendError(w, http.StatusUnauthorized, models.ErrCodeAuthExpired, "Authentication required")
			return
		}

		var notifications []models.Notification
		if err := db.Select(&notifications,
			"SELECT * FROM notifications WHERE user_id = $1 ORDER BY created_at DESC LIMIT 50",
			userID,
		); err != nil {
			logger.Error("failed to list notifications", "error", err)
			sendError(w, http.StatusInternalServerError, "internal_error", "Failed to list notifications")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(notifications)
	}
}

func handleMarkNotificationRead(db *dbpkg.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := auth.MustGetUserID(r.Context())
		if err != nil {
			sendError(w, http.StatusUnauthorized, models.ErrCodeAuthExpired, "Authentication required")
			return
		}

		notifID := chi.URLParam(r, "notificationID")
		if notifID == "" {
			sendError(w, http.StatusBadRequest, "invalid_request", "notificationID is required")
			return
		}

		now := time.Now()
		_, err = db.Exec(
			"UPDATE notifications SET read_at = $1 WHERE id = $2 AND user_id = $3",
			now, notifID, userID,
		)
		if err != nil {
			logger.Error("failed to mark notification read", "error", err)
			sendError(w, http.StatusInternalServerError, "internal_error", "Failed to update notification")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func handleUpdateNotificationPreferences(db *dbpkg.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := auth.MustGetUserID(r.Context())
		if err != nil {
			sendError(w, http.StatusUnauthorized, models.ErrCodeAuthExpired, "Authentication required")
			return
		}

		// TODO: Implement notification preferences
		logger.Info("notification preferences update", "user_id", userID)
		w.WriteHeader(http.StatusNoContent)
	}
}

// ============================================================================
// QUEUE HANDLERS
// ============================================================================

func handleQueueCount(redis *redispkg.Redis) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := auth.MustGetUserID(r.Context())
		if err != nil {
			sendError(w, http.StatusUnauthorized, models.ErrCodeAuthExpired, "Authentication required")
			return
		}

		count, err := redis.QueueCount(r.Context(), userID)
		if err != nil {
			logger.Error("failed to get queue count", "error", err)
			sendError(w, http.StatusInternalServerError, "internal_error", "Failed to get queue count")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int64{"count": count})
	}
}

func handleQueueVersion(redis *redispkg.Redis) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := auth.MustGetUserID(r.Context())
		if err != nil {
			sendError(w, http.StatusUnauthorized, models.ErrCodeAuthExpired, "Authentication required")
			return
		}

		version, err := redis.GetVersion(r.Context(), userID)
		if err != nil {
			logger.Error("failed to get queue version", "error", err)
			sendError(w, http.StatusInternalServerError, "internal_error", "Failed to get queue version")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int64{"version": version})
	}
}

// ============================================================================
// UTILITIES
// ============================================================================

func sendError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(models.SyncError{
		Code:    code,
		Message: message,
		Retry:   status >= 500,
	})
}
