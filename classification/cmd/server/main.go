// cmd/server/main.go — HTTP server for Classification Core
// Provides: health, readiness, metrics (Prometheus), and rule management API.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/decisionstack/classification/internal/config"
	"github.com/decisionstack/classification/internal/db"
	"github.com/decisionstack/classification/internal/health"
	"github.com/decisionstack/classification/internal/logger"
	appmiddleware "github.com/decisionstack/classification/internal/middleware"
	"github.com/decisionstack/classification/internal/redis"
	"github.com/decisionstack/classification/internal/rules"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config load: %v\n", err)
		os.Exit(1)
	}

	log := logger.New(cfg.LogLevel, cfg.LogFormat)
	log.Info("classification server starting",
		"version", "0.1.0",
		"commit", config.GitRevision,
		"build_time", config.BuildTime,
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
	ruleHandler := rules.NewHandler(ruleStore, log)

	// Health checker (no NATS conn in server mode)
	healthChecker := health.NewChecker(dbPool, redisClient, nil, log)

	// ─── HTTP Router ──────────────────────────────────────────────────────────
	r := chi.NewRouter()

	r.Use(middleware.Recoverer)
	r.Use(appmiddleware.RequestID)
	r.Use(appmiddleware.NewLogging(log))
	r.Use(appmiddleware.Recovery(log))
	r.Use(middleware.StripSlashes)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(appmiddleware.SecurityHeaders)
	if redisClient != nil {
		r.Use(appmiddleware.RateLimitMiddleware(redisClient.RawClient(), 200, 1*time.Minute))
	}

	// Health & metrics
	healthChecker.Routes(r)
	r.Handle("/metrics", promhttp.Handler())

	// API v1
	r.Route("/api/v1", func(api chi.Router) {
		api.Route("/rules", ruleHandler.Routes)
	})

	// 404 handler
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"error":"not found","path":"%s"}`+"\n", req.URL.Path)
	})

	// ─── HTTP Server ──────────────────────────────────────────────────────────
	srv := &http.Server{
		Addr:         ":" + cfg.ServerPort,
		Handler:      r,
		ReadTimeout:  cfg.ServerReadTimeout,
		WriteTimeout: cfg.ServerWriteTimeout,
	}

	// ─── Start & Graceful Shutdown ────────────────────────────────────────────
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Info("http server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("server error", "error", err)
		}
	}()

	<-shutdown
	log.Info("shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error("server shutdown error", "error", err)
	}
	log.Info("server stopped gracefully")
}
