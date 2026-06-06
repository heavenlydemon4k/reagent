// Package auth provides authentication, device session management, JWT handling,
// and middleware for the Sync & State service.
package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Store provides PostgreSQL persistence for device sessions and refresh tokens.
type Store struct {
	db *sqlx.DB
}

// NewStore creates a new Store backed by the given sqlx.DB.
func NewStore(db *sqlx.DB) *Store {
	return &Store{db: db}
}

// DB returns the underlying sqlx.DB for use in transactions.
func (s *Store) DB() *sqlx.DB {
	return s.db
}

// ---------------------------------------------------------------------------
// Device Sessions
// ---------------------------------------------------------------------------

// CreateDeviceSession inserts a new device session record and a hashed refresh token.
func (s *Store) CreateDeviceSession(ctx context.Context, userID uuid.UUID, deviceID, deviceType, deviceName string, fcmToken, apnsToken *string, refreshTokenHash string) error {
	query := `
		INSERT INTO device_sessions (id, user_id, device_id, device_type, device_name, fcm_token, apns_token, last_active_at, created_at)
		VALUES (:id, :user_id, :device_id, :device_type, :device_name, :fcm_token, :apns_token, :last_active_at, :created_at)
	`
	session := map[string]interface{}{
		"id":             uuid.New(),
		"user_id":        userID,
		"device_id":      deviceID,
		"device_type":    deviceType,
		"device_name":    deviceName,
		"fcm_token":      fcmToken,
		"apns_token":     apnsToken,
		"last_active_at": time.Now().UTC(),
		"created_at":     time.Now().UTC(),
	}
	_, err := s.db.NamedExecContext(ctx, query, session)
	if err != nil {
		return fmt.Errorf("store: create device session: %w", err)
	}

	// Store the hashed refresh token separately.
	return s.StoreRefreshToken(ctx, userID, deviceID, refreshTokenHash)
}

// GetDeviceSession retrieves a device session by user ID and device ID.
func (s *Store) GetDeviceSession(ctx context.Context, userID uuid.UUID, deviceID string) (*DeviceSessionRow, error) {
	query := `
		SELECT id, user_id, device_id, device_type, device_name, fcm_token, apns_token, last_active_at, created_at
		FROM device_sessions
		WHERE user_id = $1 AND device_id = $2
	`
	var row DeviceSessionRow
	err := s.db.GetContext(ctx, &row, query, userID, deviceID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("store: get device session: %w", err)
	}
	return &row, nil
}

// ListDeviceSessionsByUser returns all active device sessions for a user.
func (s *Store) ListDeviceSessionsByUser(ctx context.Context, userID uuid.UUID) ([]DeviceSessionRow, error) {
	query := `
		SELECT id, user_id, device_id, device_type, device_name, fcm_token, apns_token, last_active_at, created_at
		FROM device_sessions
		WHERE user_id = $1
		ORDER BY last_active_at DESC
	`
	var rows []DeviceSessionRow
	if err := s.db.SelectContext(ctx, &rows, query, userID); err != nil {
		return nil, fmt.Errorf("store: list device sessions: %w", err)
	}
	return rows, nil
}

// UpdateDeviceSessionTokens updates the FCM and APNS tokens for a device.
func (s *Store) UpdateDeviceSessionTokens(ctx context.Context, userID uuid.UUID, deviceID string, fcmToken, apnsToken *string) error {
	query := `
		UPDATE device_sessions
		SET fcm_token = :fcm_token, apns_token = :apns_token, last_active_at = :last_active_at
		WHERE user_id = :user_id AND device_id = :device_id
	`
	params := map[string]interface{}{
		"fcm_token":      fcmToken,
		"apns_token":     apnsToken,
		"last_active_at": time.Now().UTC(),
		"user_id":        userID,
		"device_id":      deviceID,
	}
	_, err := s.db.NamedExecContext(ctx, query, params)
	if err != nil {
		return fmt.Errorf("store: update device tokens: %w", err)
	}
	return nil
}

// UpdateDeviceSessionLastActive bumps the last_active_at timestamp for a device.
func (s *Store) UpdateDeviceSessionLastActive(ctx context.Context, userID uuid.UUID, deviceID string) error {
	query := `
		UPDATE device_sessions
		SET last_active_at = $1
		WHERE user_id = $2 AND device_id = $3
	`
	_, err := s.db.ExecContext(ctx, query, time.Now().UTC(), userID, deviceID)
	if err != nil {
		return fmt.Errorf("store: update last active: %w", err)
	}
	return nil
}

// DeleteDeviceSession revokes a device session and its associated refresh token.
func (s *Store) DeleteDeviceSession(ctx context.Context, userID uuid.UUID, deviceID string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin delete tx: %w", err)
	}
	defer tx.Rollback()

	// Delete refresh token first (foreign key or logical association)
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM refresh_tokens WHERE user_id = $1 AND device_id = $2`,
		userID, deviceID,
	); err != nil {
		return fmt.Errorf("store: delete refresh token: %w", err)
	}

	// Delete device session
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM device_sessions WHERE user_id = $1 AND device_id = $2`,
		userID, deviceID,
	); err != nil {
		return fmt.Errorf("store: delete device session: %w", err)
	}

	return tx.Commit()
}

// ---------------------------------------------------------------------------
// Refresh Tokens
// ---------------------------------------------------------------------------

// StoreRefreshToken stores a SHA-256 hash of a refresh token.
func (s *Store) StoreRefreshToken(ctx context.Context, userID uuid.UUID, deviceID, hash string) error {
	query := `
		INSERT INTO refresh_tokens (user_id, device_id, token_hash, created_at, expires_at)
		VALUES (:user_id, :device_id, :token_hash, :created_at, :expires_at)
		ON CONFLICT (user_id, device_id) DO UPDATE SET
			token_hash = EXCLUDED.token_hash,
			created_at = EXCLUDED.created_at,
			expires_at = EXCLUDED.expires_at
	`
	params := map[string]interface{}{
		"user_id":    userID,
		"device_id":  deviceID,
		"token_hash": hash,
		"created_at": time.Now().UTC(),
		"expires_at": time.Now().UTC().Add(30 * 24 * time.Hour), // 30 days
	}
	_, err := s.db.NamedExecContext(ctx, query, params)
	if err != nil {
		return fmt.Errorf("store: store refresh token: %w", err)
	}
	return nil
}

// GetRefreshToken retrieves the stored hash for a user's device.
func (s *Store) GetRefreshToken(ctx context.Context, userID uuid.UUID, deviceID string) (string, error) {
	query := `
		SELECT token_hash FROM refresh_tokens
		WHERE user_id = $1 AND device_id = $2 AND expires_at > $3
	`
	var hash string
	err := s.db.GetContext(ctx, &hash, query, userID, deviceID, time.Now().UTC())
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("store: get refresh token: %w", err)
	}
	return hash, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// HashRefreshToken returns a SHA-256 hex hash of a refresh token string.
func HashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// Row types (internal to store)
// ---------------------------------------------------------------------------

// DeviceSessionRow maps the device_sessions table schema.
type DeviceSessionRow struct {
	ID           uuid.UUID  `db:"id"`
	UserID       uuid.UUID  `db:"user_id"`
	DeviceID     string     `db:"device_id"`
	DeviceType   string     `db:"device_type"`
	DeviceName   string     `db:"device_name"`
	FCMToken     *string    `db:"fcm_token"`
	APNSToken    *string    `db:"apns_token"`
	LastActiveAt time.Time  `db:"last_active_at"`
	CreatedAt    time.Time  `db:"created_at"`
}
