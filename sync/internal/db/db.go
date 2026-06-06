// Package db provides PostgreSQL connection pooling for the sync service.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/decisionstack/sync/internal/config"
	"github.com/decisionstack/sync/internal/logger"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq" // PostgreSQL driver
)

// DB wraps sqlx.DB with application-specific operations.
type DB struct {
	*sqlx.DB
	cfg *config.Config
}

// New creates a new PostgreSQL connection pool.
func New(cfg *config.Config) (*DB, error) {
	db, err := sqlx.Connect("postgres", cfg.DatabaseURLWithSSL())
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}

	db.SetMaxOpenConns(cfg.DBMaxOpen)
	db.SetMaxIdleConns(cfg.DBMaxIdle)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(10 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	logger.Info("database connected", "max_open", cfg.DBMaxOpen, "max_idle", cfg.DBMaxIdle)

	return &DB{DB: db, cfg: cfg}, nil
}

// Close gracefully closes the database connection pool.
func (d *DB) Close() error {
	if d.DB != nil {
		logger.Info("closing database connection pool")
		return d.DB.Close()
	}
	return nil
}

// Health checks the database connectivity.
func (d *DB) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return d.PingContext(ctx)
}

// WithTx executes a function within a database transaction.
// Automatically rolls back on error, commits on success.
func (d *DB) WithTx(ctx context.Context, fn func(*sqlx.Tx) error) error {
	tx, err := d.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			logger.Error("transaction rollback failed", "error", rbErr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// GetUserID extracts user ID from context (set by auth middleware).
func GetUserID(ctx context.Context) (string, bool) {
	v := ctx.Value(userIDKey{})
	if v == nil {
		return "", false
	}
	uid, ok := v.(string)
	return uid, ok
}

// SetUserID stores a user ID in context.
func SetUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey{}, userID)
}

type userIDKey struct{}
