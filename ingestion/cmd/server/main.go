package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/decisionstack/ingestion/internal/backfill"
	"github.com/decisionstack/ingestion/internal/config"
	"github.com/decisionstack/ingestion/internal/crypto"
	db "github.com/decisionstack/ingestion/internal/db"
	"github.com/decisionstack/ingestion/internal/nats"
	"github.com/decisionstack/ingestion/internal/oauth"
	"github.com/decisionstack/ingestion/internal/redis"
	"github.com/google/uuid"
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
	})).With("service", "server")

	// PostgreSQL
	database, err := db.New(cfg)
	if err != nil {
		log.Error("failed to connect to database", "error", err)
		return fmt.Errorf("init database: %w", err)
	}
	defer database.Close()
	log.Info("database connected")

	// Redis
	redisClient, err := redis.New(cfg)
	if err != nil {
		log.Error("failed to connect to redis", "error", err)
		return fmt.Errorf("init redis: %w", err)
	}
	defer redisClient.Close()
	log.Info("redis connected")

	// NATS publisher
	natsPublisher, err := nats.NewPublisher(cfg.NATSURL)
	if err != nil {
		log.Error("failed to connect to NATS", "error", err)
		return fmt.Errorf("init nats: %w", err)
	}
	defer natsPublisher.Close()
	log.Info("nats connected")

	// KMS client for token encryption
	kmsClient, err := crypto.NewKMSClient(cfg)
	if err != nil {
		log.Error("failed to initialize KMS client", "error", err)
		return fmt.Errorf("init kms: %w", err)
	}
	defer kmsClient.Close()

	// Token crypto for OAuth token encryption/decryption
	tokenCrypto := crypto.NewTokenCrypto(kmsClient)

	// Token store (reused from oauth package)
	tokenStore := oauth.NewTokenStore(database.Pool(), tokenCrypto)

	// Backfill scheduler — .Client() unwraps the go-redis client
	bfScheduler := backfill.NewScheduler(redisClient.Client(), log)

	// OAuth Handler
	oauthHandler := oauth.NewHandler(
		database.Pool(),
		log,
		*tokenStore, // dereference: NewHandler expects oauth.TokenStore value, not pointer
		func(ctx context.Context, userID uuid.UUID) error {
			// TODO: substitute the real method name from your backfill package.
			// Common candidates: bfScheduler.Start(ctx, userID), bfScheduler.Schedule(ctx, userID), bfScheduler.Enqueue(ctx, userID)
			_ = ctx
			_ = userID
			_ = bfScheduler
			return nil
		},
	)

	// 4. Mount handlers to your router and start the engine
	_ = natsPublisher
	_ = oauthHandler

	log.Info("server initialized successfully")
	return nil
}