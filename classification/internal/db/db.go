// Package db provides a PostgreSQL connection pool using pq.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// Pool wraps sql.DB with lifecycle management.
type Pool struct {
	DB *sql.DB
}

// New creates a PostgreSQL pool from a DSN.
func New(dsn string, maxOpen int) (*Pool, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxOpen / 2)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(10 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &Pool{DB: db}, nil
}

// Close gracefully shuts down the pool.
func (p *Pool) Close() error {
	return p.DB.Close()
}

// Health checks connectivity.
func (p *Pool) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return p.DB.PingContext(ctx)
}

// QueryRowContext delegates to the underlying pool.
func (p *Pool) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return p.DB.QueryRowContext(ctx, query, args...)
}

// QueryContext delegates to the underlying pool.
func (p *Pool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return p.DB.QueryContext(ctx, query, args...)
}

// ExecContext delegates to the underlying pool.
func (p *Pool) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return p.DB.ExecContext(ctx, query, args...)
}
