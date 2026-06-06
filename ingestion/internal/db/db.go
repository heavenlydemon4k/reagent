// Package db provides PostgreSQL connection pool management for the Ingestion Mesh.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/decisionstack/ingestion/internal/config"

	_ "github.com/lib/pq"
)

// DB wraps sql.DB with connection pool configuration.
type DB struct {
	pool *sql.DB
}

// New creates a new PostgreSQL connection pool from configuration.
func New(cfg *config.Config) (*DB, error) {
	pool, err := sql.Open("postgres", cfg.DatabaseURLWithSSL())
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}

	pool.SetMaxOpenConns(cfg.DBMaxConns)
	pool.SetMaxIdleConns(cfg.DBMaxIdleConns)
	pool.SetConnMaxLifetime(cfg.DBConnMaxLifetime)

	// Verify connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := pool.PingContext(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &DB{pool: pool}, nil
}

// Pool returns the underlying sql.DB pool.
func (d *DB) Pool() *sql.DB {
	return d.pool
}

// Ping checks database connectivity.
func (d *DB) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return d.pool.PingContext(ctx)
}

// Close closes the connection pool.
func (d *DB) Close() error {
	return d.pool.Close()
}
