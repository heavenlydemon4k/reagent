// Package notify handles push notification dispatch via FCM, APNS, and WebSocket.
package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/decisionstack/sync/internal/logger"
	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ============================================================================
// NotificationStore — PostgreSQL persistence for notifications
// ============================================================================

// NotificationStore persists notifications and device tokens to PostgreSQL.
// All write operations use atomic transactions.
type NotificationStore struct {
	db *sqlx.DB
}

// NewNotificationStore creates a new store backed by the given database.
func NewNotificationStore(db *sqlx.DB) *NotificationStore {
	return &NotificationStore{db: db}
}

// DB returns the underlying sqlx.DB for transaction sharing.
func (s *NotificationStore) DB() *sqlx.DB {
	return s.db
}

// ============================================================================
// NOTIFICATION CRUD
// ============================================================================

// InsertNotification inserts a new notification record.
func (s *NotificationStore) InsertNotification(ctx context.Context, n *models.Notification) error {
	if n.ID == uuid.Nil {
		n.ID = uuid.New()
	}
	n.CreatedAt = time.Now().UTC()

	query := `
		INSERT INTO notifications (id, user_id, type, title, body, data, sent_at, read_at, created_at)
		VALUES (:id, :user_id, :type, :title, :body, :data, :sent_at, :read_at, :created_at)
	`
	_, err := s.db.NamedExecContext(ctx, query, n)
	if err != nil {
		return fmt.Errorf("store: insert notification: %w", err)
	}
	return nil
}

// GetNotification retrieves a single notification by ID.
func (s *NotificationStore) GetNotification(ctx context.Context, id uuid.UUID) (*models.Notification, error) {
	var n models.Notification
	query := `SELECT id, user_id, type, title, body, data, sent_at, read_at, created_at FROM notifications WHERE id = $1`
	if err := s.db.GetContext(ctx, &n, query, id); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("store: get notification: %w", err)
	}
	return &n, nil
}

// MarkSent records that a notification was sent at the current time.
func (s *NotificationStore) MarkSent(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC()
	query := `UPDATE notifications SET sent_at = $1 WHERE id = $2`
	result, err := s.db.ExecContext(ctx, query, now, id)
	if err != nil {
		return fmt.Errorf("store: mark sent: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("store: notification not found: %s", id)
	}
	return nil
}

// MarkRead records that a notification was read by the user.
func (s *NotificationStore) MarkRead(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC()
	query := `UPDATE notifications SET read_at = $1 WHERE id = $2`
	_, err := s.db.ExecContext(ctx, query, now, id)
	if err != nil {
		return fmt.Errorf("store: mark read: %w", err)
	}
	return nil
}

// ListUnreadByUser returns all unread notifications for a user.
func (s *NotificationStore) ListUnreadByUser(ctx context.Context, userID uuid.UUID) ([]models.Notification, error) {
	var notifications []models.Notification
	query := `
		SELECT id, user_id, type, title, body, data, sent_at, read_at, created_at
		FROM notifications
		WHERE user_id = $1 AND read_at IS NULL
		ORDER BY created_at DESC
	`
	if err := s.db.SelectContext(ctx, &notifications, query, userID); err != nil {
		return nil, fmt.Errorf("store: list unread: %w", err)
	}
	return notifications, nil
}

// ============================================================================
// DEVICE SESSIONS (notification tokens)
// ============================================================================

// DeviceToken holds a push-notification token for a user's device.
type DeviceToken struct {
	UserID    uuid.UUID `db:"user_id"`
	DeviceID  string    `db:"device_id"`
	DeviceType string   `db:"device_type"` // "android", "ios"
	FCMToken  *string   `db:"fcm_token"`
	APNSToken *string   `db:"apns_token"`
}

// GetDevices returns all device sessions with push tokens for a user.
func (s *NotificationStore) GetDevices(ctx context.Context, userID uuid.UUID) ([]DeviceToken, error) {
	var devices []DeviceToken
	query := `
		SELECT user_id, device_id, device_type, fcm_token, apns_token
		FROM device_sessions
		WHERE user_id = $1
		  AND (fcm_token IS NOT NULL OR apns_token IS NOT NULL)
		ORDER BY last_active_at DESC
	`
	if err := s.db.SelectContext(ctx, &devices, query, userID); err != nil {
		return nil, fmt.Errorf("store: get devices: %w", err)
	}
	return devices, nil
}

// RemoveDeviceToken clears push tokens for a device. Called when FCM/APNS
// reports an invalid token.
func (s *NotificationStore) RemoveDeviceToken(ctx context.Context, userID uuid.UUID, deviceID string) error {
	query := `
		UPDATE device_sessions
		SET fcm_token = NULL, apns_token = NULL
		WHERE user_id = $1 AND device_id = $2
	`
	_, err := s.db.ExecContext(ctx, query, userID, deviceID)
	if err != nil {
		return fmt.Errorf("store: remove device token: %w", err)
	}
	logger.Info("removed invalid push token",
		"user_id", userID,
		"device_id", deviceID,
	)
	return nil
}

// InvalidateFCMToken clears the FCM token for a device after a failed send.
func (s *NotificationStore) InvalidateFCMToken(ctx context.Context, userID uuid.UUID, deviceID string) error {
	query := `UPDATE device_sessions SET fcm_token = NULL WHERE user_id = $1 AND device_id = $2`
	_, err := s.db.ExecContext(ctx, query, userID, deviceID)
	if err != nil {
		return fmt.Errorf("store: invalidate fcm token: %w", err)
	}
	logger.Info("invalidated FCM token", "user_id", userID, "device_id", deviceID)
	return nil
}

// InvalidateAPNSToken clears the APNS token for a device after a failed send.
func (s *NotificationStore) InvalidateAPNSToken(ctx context.Context, userID uuid.UUID, deviceID string) error {
	query := `UPDATE device_sessions SET apns_token = NULL WHERE user_id = $1 AND device_id = $2`
	_, err := s.db.ExecContext(ctx, query, userID, deviceID)
	if err != nil {
		return fmt.Errorf("store: invalidate apns token: %w", err)
	}
	logger.Info("invalidated APNS token", "user_id", userID, "device_id", deviceID)
	return nil
}

// ============================================================================
// DEFERRED NOTIFICATIONS
// ============================================================================

// DeferredNotification holds a notification that was deferred due to quiet hours.
type DeferredNotification struct {
	ID            uuid.UUID `db:"id" json:"id"`
	UserID        uuid.UUID `db:"user_id" json:"user_id"`
	Notification  json.RawMessage `db:"notification" json:"notification"`
	OriginalType  string    `db:"original_type" json:"original_type"`
	ScheduledFor  time.Time `db:"scheduled_for" json:"scheduled_for"`
	ProcessedAt   *time.Time `db:"processed_at" json:"processed_at,omitempty"`
	CreatedAt     time.Time `db:"created_at" json:"created_at"`
}

// InsertDeferred stores a notification for later delivery.
func (s *NotificationStore) InsertDeferred(ctx context.Context, userID uuid.UUID, notif *models.Notification, scheduledFor time.Time) error {
	notifData, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("store: marshal deferred notification: %w", err)
	}

	query := `
		INSERT INTO deferred_notifications (id, user_id, notification, original_type, scheduled_for, created_at)
		VALUES (:id, :user_id, :notification, :original_type, :scheduled_for, :created_at)
	`
	params := map[string]interface{}{
		"id":             uuid.New(),
		"user_id":        userID,
		"notification":   notifData,
		"original_type":  notif.Type,
		"scheduled_for":  scheduledFor,
		"created_at":     time.Now().UTC(),
	}
	_, err = s.db.NamedExecContext(ctx, query, params)
	if err != nil {
		return fmt.Errorf("store: insert deferred notification: %w", err)
	}
	return nil
}

// GetDueDeferred returns all deferred notifications that should be sent now.
func (s *NotificationStore) GetDueDeferred(ctx context.Context) ([]DeferredNotification, error) {
	var deferred []DeferredNotification
	query := `
		SELECT id, user_id, notification, original_type, scheduled_for, processed_at, created_at
		FROM deferred_notifications
		WHERE processed_at IS NULL AND scheduled_for <= $1
		ORDER BY scheduled_for ASC
		LIMIT 100
	`
	if err := s.db.SelectContext(ctx, &deferred, query, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("store: get due deferred: %w", err)
	}
	return deferred, nil
}

// MarkDeferredProcessed marks a deferred notification as processed.
func (s *NotificationStore) MarkDeferredProcessed(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE deferred_notifications SET processed_at = $1 WHERE id = $2`
	_, err := s.db.ExecContext(ctx, query, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("store: mark deferred processed: %w", err)
	}
	return nil
}

// ============================================================================
// NOTIFICATION QUEUE (Redis-backed sorted set fallback)
// ============================================================================

// QueueNotificationPriority assigns a priority score to a notification type.
// Higher score = higher priority (delivered first).
func QueueNotificationPriority(notifType string) int {
	switch notifType {
	case "interrupt":
		return 10
	case "batch":
		return 5
	case "temporal":
		return 3
	case "staging":
		return 2
	default:
		return 1
	}
}
