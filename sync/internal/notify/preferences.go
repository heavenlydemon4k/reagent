// Package notify handles push notification dispatch via FCM, APNS, and WebSocket.
package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ============================================================================
// PreferenceManager — User notification preferences
// ============================================================================

// NotificationTypePreference defines per-type user preferences.
type NotificationTypePreference struct {
	Enabled   bool `json:"enabled"`
	Vibration bool `json:"vibration"`
	Sound     bool `json:"sound"`
	Badge     bool `json:"badge"`
}

// UserPreferences holds all notification preferences for a user.
type UserPreferences struct {
	UserID uuid.UUID `db:"user_id" json:"user_id"`

	// Master switch — if false, NO notifications are sent
	NotificationsEnabled bool `db:"notifications_enabled" json:"notifications_enabled"`

	// Per-type preferences
	Batch     json.RawMessage `db:"batch_pref" json:"batch"`
	Interrupt json.RawMessage `db:"interrupt_pref" json:"interrupt"`
	Temporal  json.RawMessage `db:"temporal_pref" json:"temporal"`
	Staging   json.RawMessage `db:"staging_pref" json:"staging"`

	// Quiet hours override
	QuietHoursEnabled  bool   `db:"quiet_hours_enabled" json:"quiet_hours_enabled"`
	QuietHoursStartHour *int  `db:"quiet_hours_start" json:"quiet_hours_start,omitempty"`
	QuietHoursEndHour   *int  `db:"quiet_hours_end" json:"quiet_hours_end,omitempty"`
	Timezone           string `db:"timezone" json:"timezone"`

	// Channels
	PushEnabled   bool `db:"push_enabled" json:"push_enabled"`
	WSEnabled     bool `db:"ws_enabled" json:"ws_enabled"`
	EmailEnabled  bool `db:"email_enabled" json:"email_enabled"`

	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

// PreferenceManager reads and writes user notification preferences.
type PreferenceManager struct {
	db *sqlx.DB
}

// NewPreferenceManager creates a new preference manager.
func NewPreferenceManager(db *sqlx.DB) *PreferenceManager {
	return &PreferenceManager{db: db}
}

// GetPreferences retrieves notification preferences for a user.
// Returns default preferences if the user has no stored preferences.
func (pm *PreferenceManager) GetPreferences(ctx context.Context, userID uuid.UUID) (*UserPreferences, error) {
	var prefs UserPreferences
	query := `
		SELECT user_id, notifications_enabled,
		       batch_pref, interrupt_pref, temporal_pref, staging_pref,
		       quiet_hours_enabled, quiet_hours_start, quiet_hours_end, timezone,
		       push_enabled, ws_enabled, email_enabled,
		       created_at, updated_at
		FROM user_notification_preferences
		WHERE user_id = $1
	`
	if err := pm.db.GetContext(ctx, &prefs, query, userID); err != nil {
		if err == sql.ErrNoRows {
			return pm.defaultPreferences(userID), nil
		}
		return nil, fmt.Errorf("prefs: get preferences: %w", err)
	}
	return &prefs, nil
}

// GetTypePreference extracts a typed preference for a notification type.
func (pm *PreferenceManager) GetTypePreference(ctx context.Context, userID uuid.UUID, notifType string) (*NotificationTypePreference, error) {
	prefs, err := pm.GetPreferences(ctx, userID)
	if err != nil {
		return nil, err
	}

	var raw json.RawMessage
	switch notifType {
	case "batch":
		raw = prefs.Batch
	case "interrupt":
		raw = prefs.Interrupt
	case "temporal":
		raw = prefs.Temporal
	case "staging":
		raw = prefs.Staging
	default:
		return &NotificationTypePreference{Enabled: true, Sound: true, Badge: true}, nil
	}

	if len(raw) == 0 {
		return &NotificationTypePreference{Enabled: true, Sound: true, Badge: true}, nil
	}

	var typePref NotificationTypePreference
	if err := json.Unmarshal(raw, &typePref); err != nil {
		return &NotificationTypePreference{Enabled: true, Sound: true, Badge: true}, nil
	}
	return &typePref, nil
}

// IsTypeEnabled returns true if notifications of the given type are enabled for the user.
func (pm *PreferenceManager) IsTypeEnabled(ctx context.Context, userID uuid.UUID, notifType string) bool {
	// Master switch
	master, err := pm.IsMasterEnabled(ctx, userID)
	if err != nil || !master {
		return false
	}

	typePref, err := pm.GetTypePreference(ctx, userID, notifType)
	if err != nil {
		return true // default to enabled on error
	}

	return typePref.Enabled
}

// IsMasterEnabled returns the master notification switch for a user.
func (pm *PreferenceManager) IsMasterEnabled(ctx context.Context, userID uuid.UUID) (bool, error) {
	prefs, err := pm.GetPreferences(ctx, userID)
	if err != nil {
		return false, err
	}
	return prefs.NotificationsEnabled, nil
}

// IsPushEnabled returns true if push notifications are enabled for the user.
func (pm *PreferenceManager) IsPushEnabled(ctx context.Context, userID uuid.UUID) (bool, error) {
	prefs, err := pm.GetPreferences(ctx, userID)
	if err != nil {
		return false, err
	}
	return prefs.PushEnabled && prefs.NotificationsEnabled, nil
}

// IsWSEnabled returns true if WebSocket notifications are enabled for the user.
func (pm *PreferenceManager) IsWSEnabled(ctx context.Context, userID uuid.UUID) (bool, error) {
	prefs, err := pm.GetPreferences(ctx, userID)
	if err != nil {
		return false, err
	}
	return prefs.WSEnabled && prefs.NotificationsEnabled, nil
}

// SetPreferences upserts user notification preferences.
func (pm *PreferenceManager) SetPreferences(ctx context.Context, prefs *UserPreferences) error {
	now := time.Now().UTC()
	prefs.UpdatedAt = now
	if prefs.CreatedAt.IsZero() {
		prefs.CreatedAt = now
	}

	query := `
		INSERT INTO user_notification_preferences (
			user_id, notifications_enabled,
			batch_pref, interrupt_pref, temporal_pref, staging_pref,
			quiet_hours_enabled, quiet_hours_start, quiet_hours_end, timezone,
			push_enabled, ws_enabled, email_enabled,
			created_at, updated_at
		) VALUES (
			:user_id, :notifications_enabled,
			:batch_pref, :interrupt_pref, :temporal_pref, :staging_pref,
			:quiet_hours_enabled, :quiet_hours_start, :quiet_hours_end, :timezone,
			:push_enabled, :ws_enabled, :email_enabled,
			:created_at, :updated_at
		)
		ON CONFLICT (user_id) DO UPDATE SET
			notifications_enabled = EXCLUDED.notifications_enabled,
			batch_pref             = EXCLUDED.batch_pref,
			interrupt_pref         = EXCLUDED.interrupt_pref,
			temporal_pref          = EXCLUDED.temporal_pref,
			staging_pref           = EXCLUDED.staging_pref,
			quiet_hours_enabled    = EXCLUDED.quiet_hours_enabled,
			quiet_hours_start      = EXCLUDED.quiet_hours_start,
			quiet_hours_end        = EXCLUDED.quiet_hours_end,
			timezone               = EXCLUDED.timezone,
			push_enabled           = EXCLUDED.push_enabled,
			ws_enabled             = EXCLUDED.ws_enabled,
			email_enabled          = EXCLUDED.email_enabled,
			updated_at             = EXCLUDED.updated_at
	`
	_, err := pm.db.NamedExecContext(ctx, query, prefs)
	if err != nil {
		return fmt.Errorf("prefs: set preferences: %w", err)
	}
	return nil
}

// defaultPreferences returns sensible defaults for a new user.
func (pm *PreferenceManager) defaultPreferences(userID uuid.UUID) *UserPreferences {
	batchPref, _ := json.Marshal(&NotificationTypePreference{Enabled: true, Sound: true, Badge: true})
	interruptPref, _ := json.Marshal(&NotificationTypePreference{Enabled: true, Vibration: true, Sound: true, Badge: true})
	temporalPref, _ := json.Marshal(&NotificationTypePreference{Enabled: true, Sound: false, Badge: true})
	stagingPref, _ := json.Marshal(&NotificationTypePreference{Enabled: true, Sound: true, Badge: false})

	return &UserPreferences{
		UserID:                userID,
		NotificationsEnabled:  true,
		Batch:                 batchPref,
		Interrupt:             interruptPref,
		Temporal:              temporalPref,
		Staging:               stagingPref,
		QuietHoursEnabled:     true,
		QuietHoursStartHour:   intPtr(22),
		QuietHoursEndHour:     intPtr(7),
		Timezone:              "America/New_York",
		PushEnabled:           true,
		WSEnabled:             true,
		EmailEnabled:          false,
		CreatedAt:             time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
}

func intPtr(v int) *int {
	return &v
}
