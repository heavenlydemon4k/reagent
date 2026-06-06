package auth

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/decisionstack/sync/internal/models"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ---------------------------------------------------------------------------
// DeviceManager
// ---------------------------------------------------------------------------

// DeviceManager provides high-level CRUD operations for device sessions,
// backed by Store.  It bridges the internal store layer with the models
// types used by handlers.
type DeviceManager struct {
	store *Store
}

// NewDeviceManager creates a new DeviceManager.
func NewDeviceManager(db *sqlx.DB) *DeviceManager {
	return &DeviceManager{store: NewStore(db)}
}

// Store returns the underlying Store (exposed for transaction sharing).
func (dm *DeviceManager) Store() *Store {
	return dm.store
}

// Register creates a new device session for a user.  If a session for the
// same (user_id, device_id) pair already exists it will be replaced (the
// old refresh token is overwritten).
func (dm *DeviceManager) Register(ctx context.Context, session *models.DeviceSession) error {
	if session.UserID == uuid.Nil {
		return fmt.Errorf("auth: user_id is required")
	}
	if session.DeviceID == "" {
		return fmt.Errorf("auth: device_id is required")
	}
	if session.DeviceType != "ios" && session.DeviceType != "android" {
		return fmt.Errorf("auth: device_type must be 'ios' or 'android'")
	}

	now := time.Now().UTC()
	session.LastActiveAt = now
	session.CreatedAt = now

	query := `
		INSERT INTO device_sessions (id, user_id, device_id, device_type, device_name, fcm_token, apns_token, last_active_at, created_at)
		VALUES (:id, :user_id, :device_id, :device_type, :device_name, :fcm_token, :apns_token, :last_active_at, :created_at)
		ON CONFLICT (user_id, device_id) DO UPDATE SET
			device_type   = EXCLUDED.device_type,
			device_name   = EXCLUDED.device_name,
			fcm_token     = EXCLUDED.fcm_token,
			apns_token    = EXCLUDED.apns_token,
			last_active_at = EXCLUDED.last_active_at
	`
	params := map[string]interface{}{
		"id":             session.ID,
		"user_id":        session.UserID,
		"device_id":      session.DeviceID,
		"device_type":    session.DeviceType,
		"device_name":    session.DeviceName,
		"fcm_token":      session.FCMToken,
		"apns_token":     session.APNSToken,
		"last_active_at": session.LastActiveAt,
		"created_at":     session.CreatedAt,
	}
	_, err := dm.store.db.NamedExecContext(ctx, query, params)
	if err != nil {
		return fmt.Errorf("auth: register device session: %w", err)
	}
	return nil
}

// GetByDeviceID retrieves a single device session by user and device ID.
func (dm *DeviceManager) GetByDeviceID(ctx context.Context, userID uuid.UUID, deviceID string) (*models.DeviceSession, error) {
	row, err := dm.store.GetDeviceSession(ctx, userID, deviceID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	return rowToModel(row), nil
}

// ListByUser returns all device sessions belonging to a user, ordered by
// last_active_at descending.
func (dm *DeviceManager) ListByUser(ctx context.Context, userID uuid.UUID) ([]models.DeviceSession, error) {
	rows, err := dm.store.ListDeviceSessionsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	sessions := make([]models.DeviceSession, len(rows))
	for i, r := range rows {
		sessions[i] = *rowToModel(&r)
	}
	return sessions, nil
}

// UpdateTokens updates push-notification tokens for a device.
func (dm *DeviceManager) UpdateTokens(ctx context.Context, userID uuid.UUID, deviceID string, fcmToken, apnsToken *string) error {
	return dm.store.UpdateDeviceSessionTokens(ctx, userID, deviceID, fcmToken, apnsToken)
}

// Revoke deletes a device session and its associated refresh token atomically.
func (dm *DeviceManager) Revoke(ctx context.Context, userID uuid.UUID, deviceID string) error {
	return dm.store.DeleteDeviceSession(ctx, userID, deviceID)
}

// UpdateLastActive bumps the last_active_at timestamp for a device session.
func (dm *DeviceManager) UpdateLastActive(ctx context.Context, userID uuid.UUID, deviceID string) error {
	return dm.store.UpdateDeviceSessionLastActive(ctx, userID, deviceID)
}

// ---------------------------------------------------------------------------
// Row mapping
// ---------------------------------------------------------------------------

func rowToModel(r *DeviceSessionRow) *models.DeviceSession {
	return &models.DeviceSession{
		ID:           r.ID,
		UserID:       r.UserID,
		DeviceID:     r.DeviceID,
		DeviceType:   r.DeviceType,
		DeviceName:   r.DeviceName,
		FCMToken:     r.FCMToken,
		APNSToken:    r.APNSToken,
		LastActiveAt: r.LastActiveAt,
		CreatedAt:    r.CreatedAt,
	}
}

// ErrDeviceNotFound is returned when a device session does not exist.
var ErrDeviceNotFound = fmt.Errorf("auth: device session not found")

// IsNotFound checks whether an error indicates a missing row.
func IsNotFound(err error) bool {
	return err == sql.ErrNoRows || err == ErrDeviceNotFound
}
