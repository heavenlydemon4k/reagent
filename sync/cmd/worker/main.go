// cmd/worker/main.go — Background worker entry point for push notifications and queue processing.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/decisionstack/sync/internal/config"
	"github.com/decisionstack/sync/internal/logger"
	natspkg "github.com/decisionstack/sync/internal/nats"
)

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
	logger.Info("sync worker starting", "environment", cfg.Environment)

	// ============================================================================
	// INITIALIZE NATS PUBLISHER
	// ============================================================================
	natsPublisher, err := natspkg.NewPublisher(cfg)
	if err != nil {
		logger.Error("failed to connect to nats", "error", err)
		// Worker can continue without NATS, but functionality will be limited
	} else {
		defer natsPublisher.Close()
	}

	// ============================================================================
	// CONTEXT & SIGNAL HANDLING
	// ============================================================================
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	// ============================================================================
	// START WORKER GOROUTINES
	// ============================================================================

	// Push notification dispatcher
	go runNotificationDispatcher(ctx, cfg, natsPublisher)

	// Queue maintenance worker
	go runQueueMaintenance(ctx, cfg)

	// Sync log cleanup worker
	go runSyncLogCleanup(ctx, cfg)

	logger.Info("sync worker running")

	// Wait for shutdown signal
	<-done
	logger.Info("worker shutting down...")
	cancel()

	// Allow goroutines to clean up
	time.Sleep(1 * time.Second)
	logger.Info("worker stopped gracefully")
}

// runNotificationDispatcher periodically processes pending push notifications.
func runNotificationDispatcher(ctx context.Context, cfg *config.Config, pub *natspkg.Publisher) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("notification dispatcher stopping")
			return
		case <-ticker.C:
			if err := dispatchNotifications(ctx, cfg); err != nil {
				logger.Error("notification dispatch failed", "error", err)
			}
		}
	}
}

// dispatchNotifications sends pending push notifications respecting quiet hours.
func dispatchNotifications(ctx context.Context, cfg *config.Config) error {
	now := time.Now()
	hour := now.Hour()

	// Check quiet hours
	isQuietHours := false
	if cfg.QuietHoursStart > cfg.QuietHoursEnd {
		// Wraps around midnight (e.g., 22:00 - 08:00)
		isQuietHours = hour >= cfg.QuietHoursStart || hour < cfg.QuietHoursEnd
	} else {
		isQuietHours = hour >= cfg.QuietHoursStart && hour < cfg.QuietHoursEnd
	}

	if isQuietHours {
		logger.Debug("quiet hours active, skipping notification dispatch",
			"hour", hour,
			"quiet_start", cfg.QuietHoursStart,
			"quiet_end", cfg.QuietHoursEnd,
		)
		return nil
	}

	// TODO: Query pending notifications from database
	// TODO: Check user preferences (per-user quiet hours, DND)
	// TODO: Send via FCM for Android devices
	// TODO: Send via APNS for iOS devices
	// TODO: Mark notifications as sent

	return nil
}

// runQueueMaintenance performs periodic queue maintenance tasks.
func runQueueMaintenance(ctx context.Context, cfg *config.Config) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("queue maintenance stopping")
			return
		case <-ticker.C:
			logger.Debug("running queue maintenance")
			// TODO: Clean up stale queue entries
			// TODO: Rebuild Redis queues from PostgreSQL if needed
			// TODO: Expire old decision cards
		}
	}
}

// runSyncLogCleanup periodically cleans up old sync log entries.
func runSyncLogCleanup(ctx context.Context, cfg *config.Config) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("sync log cleanup stopping")
			return
		case <-ticker.C:
			logger.Debug("running sync log cleanup")
			// TODO: Delete sync_log entries older than retention period
		}
	}
}
