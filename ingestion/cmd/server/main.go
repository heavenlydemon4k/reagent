// cmd/server/main.go — HTTP server entry point for the Ingestion Mesh.
//
// Initialises all dependencies (Postgres, Redis, NATS, KMS, Neo4j) and
// starts the chi-based HTTP server on cfg.ServerPort.
//
// Phase 0: server starts and /health returns 200.
// Phase 1: webhook and OAuth handlers are fully wired.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/google/uuid"

	"github.com/decisionstack/ingestion/internal/backfill"
	"github.com/decisionstack/ingestion/internal/config"
	"github.com/decisionstack/ingestion/internal/crypto"
	"github.com/decisionstack/ingestion/internal/db"
	"github.com/decisionstack/ingestion/internal/fetch"
	"github.com/decisionstack/ingestion/internal/nats"
	"github.com/decisionstack/ingestion/internal/oauth"
	"github.com/decisionstack/ingestion/internal/redis"
	"github.com/decisionstack/ingestion/internal/server"
	"github.com/decisionstack/ingestion/internal/webhook"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})).With("service", "ingestion")

	// ── PostgreSQL ────────────────────────────────────────────────────────────
	database, err := db.New(cfg)
	if err != nil {
		log.Error("failed to connect to database", "error", err)
		return fmt.Errorf("init database: %w", err)
	}
	defer database.Close()
	log.Info("database connected")

	// ── Redis ─────────────────────────────────────────────────────────────────
	redisClient, err := redis.New(cfg)
	if err != nil {
		log.Error("failed to connect to redis", "error", err)
		return fmt.Errorf("init redis: %w", err)
	}
	defer redisClient.Close()
	log.Info("redis connected")

	// ── NATS ──────────────────────────────────────────────────────────────────
	natsPublisher, err := nats.NewPublisher(cfg.NATSURL)
	if err != nil {
		log.Error("failed to connect to NATS", "error", err)
		return fmt.Errorf("init nats: %w", err)
	}
	defer natsPublisher.Close()
	log.Info("nats connected")

	// ── KMS ───────────────────────────────────────────────────────────────────
	kmsClient, err := crypto.NewKMSClient(cfg)
	if err != nil {
		log.Error("failed to initialise KMS client", "error", err)
		return fmt.Errorf("init kms: %w", err)
	}
	defer kmsClient.Close()
	log.Info("kms initialised")

	// ── OAuth token crypto & store ────────────────────────────────────────────
	tokenCrypto := crypto.NewTokenCrypto(kmsClient)
	tokenStore := oauth.NewTokenStore(database.Pool(), tokenCrypto)

	// ── Backfill scheduler ────────────────────────────────────────────────────
	bfScheduler := backfill.NewScheduler(redisClient.Client(), log)

	// ── OAuth handler ─────────────────────────────────────────────────────────
	oauthHandler := oauth.NewHandler(
		database.Pool(),
		log,
		tokenStore,
		cfg,
		redisClient.Client(),
		func(ctx context.Context, userID uuid.UUID) error {
			job := &backfill.BackfillJob{UserID: userID}
			return bfScheduler.Enqueue(ctx, job)
		},
	)

	// ── Webhook handler ───────────────────────────────────────────────────────
	enqueuer := fetch.NewEnqueuer(redisClient.Client(), natsPublisher, log)
	webhookHandler := webhook.NewHandler(cfg, redisClient.Client(), natsPublisher, enqueuer, log)

	// ── Server ────────────────────────────────────────────────────────────────
	sendHandler := server.NewSendHandler(natsPublisher)
	deps := &server.Dependencies{
		Log:            log,
		Config:         cfg,
		WebhookHandler: webhookHandler,
		OAuthHandler:   oauthHandler,
		NATSPublisher:  natsPublisher,
		SendHandler:    sendHandler,
	}

	srv := server.NewServer(cfg, deps)
	log.Info("starting ingestion server", "addr", fmt.Sprintf("%s:%s", cfg.ServerHost, cfg.ServerPort))
	return srv.Run()
}
