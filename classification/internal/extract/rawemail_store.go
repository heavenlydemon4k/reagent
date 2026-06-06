package extract

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/decisionstack/classification/internal/db"
	"github.com/google/uuid"
)

// RawEmailDB implements RawEmailStore using the shared PostgreSQL pool.
type RawEmailDB struct {
	pool *db.Pool
}

// NewRawEmailDB creates a RawEmailStore backed by PostgreSQL.
func NewRawEmailDB(pool *db.Pool) *RawEmailDB {
	return &RawEmailDB{pool: pool}
}

// FetchBody returns the subject and plain-text body for a raw email.
func (s *RawEmailDB) FetchBody(ctx context.Context, rawEmailID uuid.UUID) (string, string, error) {
	var subject, body string
	err := s.pool.QueryRowContext(ctx, `
		SELECT subject, body_text
		FROM raw_emails
		WHERE id = $1
	`, rawEmailID).Scan(&subject, &body)
	if err == sql.ErrNoRows {
		return "", "", fmt.Errorf("raw_email %s not found: %w", rawEmailID, err)
	}
	if err != nil {
		return "", "", fmt.Errorf("fetch body for %s: %w", rawEmailID, err)
	}
	return subject, body, nil
}
