package poll

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// StateStore persists and retrieves polling state (history_id for Gmail,
// delta_link for Outlook) from PostgreSQL. All updates are atomic with the
// raw_emails INSERT via transaction support.
type StateStore struct {
	db *sql.DB
}

// NewStateStore creates a new StateStore backed by the given database.
func NewStateStore(db *sql.DB) *StateStore {
	return &StateStore{db: db}
}

// DB returns the underlying database handle for use in transactions.
func (s *StateStore) DB() *sql.DB {
	return s.db
}

// GetHistoryID retrieves the stored Gmail historyId for an account.
// Returns empty string if no historyId is set (initial sync needed).
func (s *StateStore) GetHistoryID(ctx context.Context, accountID uuid.UUID) (string, error) {
	var historyID sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT history_id FROM email_accounts WHERE id = $1`,
		accountID,
	).Scan(&historyID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("account not found: %s", accountID)
		}
		return "", fmt.Errorf("get history_id: %w", err)
	}
	if !historyID.Valid {
		return "", nil
	}
	return historyID.String, nil
}

// UpdateHistoryID sets the Gmail historyId for an account atomically within
// a transaction. This MUST be called inside a transaction that also inserts
// into raw_emails to ensure zero email loss.
func (s *StateStore) UpdateHistoryID(ctx context.Context, tx *sql.Tx, accountID uuid.UUID, historyID string) error {
	if historyID == "" {
		return fmt.Errorf("historyID cannot be empty")
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE email_accounts SET history_id = $1, updated_at = $2 WHERE id = $3`,
		historyID, time.Now().UTC(), accountID,
	)
	if err != nil {
		return fmt.Errorf("update history_id: %w", err)
	}
	return nil
}

// UpdateHistoryIDDirect updates history_id without a transaction.
// Use this only when no raw_emails insert is involved.
func (s *StateStore) UpdateHistoryIDDirect(ctx context.Context, accountID uuid.UUID, historyID string) error {
	if historyID == "" {
		return fmt.Errorf("historyID cannot be empty")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE email_accounts SET history_id = $1, updated_at = $2 WHERE id = $3`,
		historyID, time.Now().UTC(), accountID,
	)
	if err != nil {
		return fmt.Errorf("update history_id direct: %w", err)
	}
	return nil
}

// GetDeltaLink retrieves the stored Outlook deltaLink for an account.
// Returns empty string if no deltaLink is set (initial sync needed).
func (s *StateStore) GetDeltaLink(ctx context.Context, accountID uuid.UUID) (string, error) {
	var deltaLink sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT delta_link FROM email_accounts WHERE id = $1`,
		accountID,
	).Scan(&deltaLink)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("account not found: %s", accountID)
		}
		return "", fmt.Errorf("get delta_link: %w", err)
	}
	if !deltaLink.Valid {
		return "", nil
	}
	return deltaLink.String, nil
}

// UpdateDeltaLink sets the Outlook deltaLink for an account atomically within
// a transaction. This MUST be called inside a transaction that also inserts
// into raw_emails to ensure zero email loss.
func (s *StateStore) UpdateDeltaLink(ctx context.Context, tx *sql.Tx, accountID uuid.UUID, deltaLink string) error {
	if deltaLink == "" {
		return fmt.Errorf("deltaLink cannot be empty")
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE email_accounts SET delta_link = $1, updated_at = $2 WHERE id = $3`,
		deltaLink, time.Now().UTC(), accountID,
	)
	if err != nil {
		return fmt.Errorf("update delta_link: %w", err)
	}
	return nil
}

// UpdateDeltaLinkDirect updates delta_link without a transaction.
// Use this only when no raw_emails insert is involved.
func (s *StateStore) UpdateDeltaLinkDirect(ctx context.Context, accountID uuid.UUID, deltaLink string) error {
	if deltaLink == "" {
		return fmt.Errorf("deltaLink cannot be empty")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE email_accounts SET delta_link = $1, updated_at = $2 WHERE id = $3`,
		deltaLink, time.Now().UTC(), accountID,
	)
	if err != nil {
		return fmt.Errorf("update delta_link direct: %w", err)
	}
	return nil
}

// AtomicEmailCommit persists a raw email and updates the polling state
// (history_id or delta_link) in a single transaction. This is the core
// mechanism that guarantees zero email loss.
func (s *StateStore) AtomicEmailCommit(
	ctx context.Context,
	insertEmail func(tx *sql.Tx) error,
	updateState func(tx *sql.Tx) error,
) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	// Rollback on panic
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	// Insert the raw email
	if err := insertEmail(tx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("insert email: %w", err)
	}

	// Update the polling state
	if err := updateState(tx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("update state: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}
