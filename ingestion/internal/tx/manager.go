// Package tx provides database transaction management for the Ingestion Mesh.
// manager.go wraps sql.DB with atomic transaction helpers used by the
// assembler to ensure thread upsert + raw_emails INSERT + state update are atomic.
package tx

import (
	"context"
	"database/sql"
	"fmt"
)

// Manager wraps a *sql.DB and provides convenient transaction handling.
type Manager struct {
	db *sql.DB
}

// NewManager creates a new transaction manager.
func NewManager(db *sql.DB) *Manager {
	return &Manager{db: db}
}

// Begin starts a new transaction with the given context.
func (m *Manager) Begin(ctx context.Context) (*sql.Tx, error) {
	return m.db.BeginTx(ctx, nil)
}

// Commit commits the given transaction.
func (m *Manager) Commit(tx *sql.Tx) error {
	if tx == nil {
		return fmt.Errorf("tx is nil")
	}
	return tx.Commit()
}

// Rollback rolls back the given transaction. It is safe to call on a nil tx
// or on a transaction that has already been committed/rolled back.
func (m *Manager) Rollback(tx *sql.Tx) error {
	if tx == nil {
		return nil
	}
	return tx.Rollback()
}

// InTx executes the given function inside a transaction.
// It begins a transaction, runs fn, and commits if fn returns nil.
// If fn returns an error or panic occurs, the transaction is rolled back.
func (m *Manager) InTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := m.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	// Ensure rollback on panic or error
	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback()
			panic(r) // re-panic after rollback
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("fn error: %w; rollback also failed: %v", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// DB returns the underlying *sql.DB.
func (m *Manager) DB() *sql.DB {
	return m.db
}
