// cmd/worker/main.go — NATS consumer for Classification Core
// Subscribes to email.ingested, classifies each email, and publishes the result.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/decisionstack/classification/internal/classifier"
	"github.com/decisionstack/classification/internal/config"
	"github.com/decisionstack/classification/internal/db"
	"github.com/decisionstack/classification/internal/extract"
	"github.com/decisionstack/classification/internal/logger"
	appnats "github.com/decisionstack/classification/internal/nats"
	"github.com/decisionstack/classification/internal/redis"
	"github.com/decisionstack/classification/internal/rules"
	"github.com/nats-io/nats.go"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config load: %v\n", err)
		os.Exit(1)
	}

	log := logger.New(cfg.LogLevel, cfg.LogFormat)
	log.Info("classification worker starting",
		"consumer", cfg.NATSConsumerName,
		"subject", cfg.NATSSubjectEmail,
		"version", "0.1.0",
		"commit", config.GitRevision,
	)

	// ─── Dependencies ─────────────────────────────────────────────────────────
	dbPool, err := db.New(cfg.DSNWithSSL(), cfg.DBPoolMax)
	if err != nil {
		log.Fatal("db connect failed", "error", err)
	}
	defer dbPool.Close()

	var redisClient *redis.Client
	if cfg.RedisAddr != "" {
		redisClient, err = redis.New(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
		if err != nil {
			log.Warn("redis connect failed, continuing without cache", "error", err)
		}
		defer redisClient.Close()
	}

	ruleStore := rules.NewStore(dbPool)

	// Classification engine (LLM client nil in scaffold — will be wired via AWS Bedrock)
	engine := classifier.NewEngine(ruleStore, cfg, log, nil)

	// Wire Extract-Only pipeline (regex fast-path for 2FA, tracking, receipts, calendar)
	rawEmailDB := extract.NewRawEmailDB(dbPool)
	extractor := extract.NewExtractor(rawEmailDB, nil, nil, nil)
	engine.SetExtractor(extractor)

	// ─── NATS ─────────────────────────────────────────────────────────────────
	nc, err := nats.Connect(cfg.NATSURL,
		nats.Name(cfg.NATSConsumerName+"-conn"),
		nats.ReconnectWait(2*time.Second),
		nats.MaxReconnects(10),
	)
	if err != nil {
		log.Fatal("nats connect", "error", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		log.Fatal("jetstream context", "error", err)
	}

	publisher := appnats.NewPublisher(
		js,
		cfg.NATSSubjectIntelligence,
		cfg.NATSSubjectExtracted,
		cfg.NATSSubjectAuto,
		log,
	)

	consumer, err := appnats.NewConsumerFromConn(nc, js, cfg, log, engine, publisher)
	if err != nil {
		log.Fatal("nats consumer", "error", err)
	}

	// ─── Graceful Shutdown ────────────────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		errCh <- consumer.Subscribe(ctx)
	}()

	select {
	case sig := <-shutdown:
		log.Info("shutdown signal received", "signal", sig)
		cancel()
	case err := <-errCh:
		if err != nil {
			log.Error("consumer error", "error", err)
		}
		cancel()
	}

	// Give in-flight messages time to ack
	log.Info("draining in-flight messages...")
	time.Sleep(5 * time.Second)
	log.Info("worker stopped")
}
