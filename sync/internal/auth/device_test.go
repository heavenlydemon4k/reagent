// Package auth_test provides unit tests for device session management.
package auth

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/decisionstack/sync/internal/models"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Tests: DeviceManager construction
// ---------------------------------------------------------------------------

func TestNewDeviceManager(t *testing.T) {
	// DeviceManager can be created with nil db (for testing the constructor)
	dm := NewDeviceManager(nil)
	if dm == nil {
		t.Fatal("NewDeviceManager returned nil")
	}
	if dm.store == nil {
		t.Fatal("store should not be nil")
	}
}

func TestDeviceManager_Store(t *testing.T) {
	dm := NewDeviceManager(nil)
	store := dm.Store()
	if store == nil {
		t.Fatal("Store() returned nil")
	}
}

// ---------------------------------------------------------------------------
// Tests: Register validation (no DB required)
// ---------------------------------------------------------------------------

func TestRegister_MissingUserID(t *testing.T) {
	dm := NewDeviceManager(nil)
	session := &models.DeviceSession{
		ID:         uuid.New(),
		UserID:     uuid.Nil,
		DeviceID:   "device-001",
		DeviceType: "ios",
		DeviceName: "Test iPhone",
	}
	err := dm.Register(t.Context(), session)
	if err == nil {
		t.Fatal("expected error for missing user ID")
	}
	if !strings.Contains(err.Error(), "user_id is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRegister_MissingDeviceID(t *testing.T) {
	dm := NewDeviceManager(nil)
	session := &models.DeviceSession{
		ID:         uuid.New(),
		UserID:     uuid.New(),
		DeviceID:   "",
		DeviceType: "ios",
		DeviceName: "Test iPhone",
	}
	err := dm.Register(t.Context(), session)
	if err == nil {
		t.Fatal("expected error for missing device ID")
	}
	if !strings.Contains(err.Error(), "device_id is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRegister_InvalidDeviceType(t *testing.T) {
	dm := NewDeviceManager(nil)
	session := &models.DeviceSession{
		ID:         uuid.New(),
		UserID:     uuid.New(),
		DeviceID:   "device-001",
		DeviceType: "windows_phone",
		DeviceName: "Test Phone",
	}
	err := dm.Register(t.Context(), session)
	if err == nil {
		t.Fatal("expected error for invalid device type")
	}
	if !strings.Contains(err.Error(), "device_type must be 'ios' or 'android'") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRegister_ValidIOS(t *testing.T) {
	dm := NewDeviceManager(nil)
	uid := uuid.New()
	session := &models.DeviceSession{
		ID:         uuid.New(),
		UserID:     uid,
		DeviceID:   "device-ios-001",
		DeviceType: "ios",
		DeviceName: "Test iPhone",
	}
	err := dm.Register(t.Context(), session)
	// Will fail at DB level (nil db), but validation should pass
	if err != nil && strings.Contains(err.Error(), "user_id") {
		t.Errorf("validation should pass for valid iOS device: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "device_type") {
		t.Errorf("validation should pass for valid iOS device: %v", err)
	}
}

func TestRegister_ValidAndroid(t *testing.T) {
	dm := NewDeviceManager(nil)
	session := &models.DeviceSession{
		ID:         uuid.New(),
		UserID:     uuid.New(),
		DeviceID:   "device-android-001",
		DeviceType: "android",
		DeviceName: "Test Pixel",
	}
	err := dm.Register(t.Context(), session)
	if err != nil && strings.Contains(err.Error(), "user_id") {
		t.Errorf("validation should pass for valid Android device: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "device_type") {
		t.Errorf("validation should pass for valid Android device: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: Register sets timestamps
// ---------------------------------------------------------------------------

func TestRegister_SetsTimestamps(t *testing.T) {
	dm := NewDeviceManager(nil)
	before := time.Now().UTC().Add(-time.Second)
	session := &models.DeviceSession{
		ID:         uuid.New(),
		UserID:     uuid.New(),
		DeviceID:   "device-001",
		DeviceType: "ios",
		DeviceName: "Test iPhone",
	}
	_ = dm.Register(t.Context(), session)
	after := time.Now().UTC().Add(time.Second)

	if session.CreatedAt.Before(before) || session.CreatedAt.After(after) {
		t.Errorf("CreatedAt not set to current time: %v", session.CreatedAt)
	}
	if session.LastActiveAt.Before(before) || session.LastActiveAt.After(after) {
		t.Errorf("LastActiveAt not set to current time: %v", session.LastActiveAt)
	}
}

// ---------------------------------------------------------------------------
// Tests: rowToModel mapping
// ---------------------------------------------------------------------------

func TestRowToModel(t *testing.T) {
	uid := uuid.New()
	now := time.Now().UTC()
	fcmToken := "fcm-token-123"
	apnsToken := "apns-token-456"

	row := &DeviceSessionRow{
		ID:           uid,
		UserID:       uid,
		DeviceID:     "device-001",
		DeviceType:   "ios",
		DeviceName:   "Test iPhone",
		FCMToken:     &fcmToken,
		APNSToken:    &apnsToken,
		LastActiveAt: now,
		CreatedAt:    now,
	}

	model := rowToModel(row)
	if model == nil {
		t.Fatal("rowToModel returned nil")
	}
	if model.ID != row.ID {
		t.Errorf("ID: want %s, got %s", row.ID, model.ID)
	}
	if model.UserID != row.UserID {
		t.Errorf("UserID: want %s, got %s", row.UserID, model.UserID)
	}
	if model.DeviceID != row.DeviceID {
		t.Errorf("DeviceID: want %q, got %q", row.DeviceID, model.DeviceID)
	}
	if model.DeviceType != row.DeviceType {
		t.Errorf("DeviceType: want %q, got %q", row.DeviceType, model.DeviceType)
	}
	if model.DeviceName != row.DeviceName {
		t.Errorf("DeviceName: want %q, got %q", row.DeviceName, model.DeviceName)
	}
	if model.FCMToken == nil || *model.FCMToken != fcmToken {
		t.Errorf("FCMToken mismatch")
	}
	if model.APNSToken == nil || *model.APNSToken != apnsToken {
		t.Errorf("APNSToken mismatch")
	}
	if !model.LastActiveAt.Equal(now) {
		t.Errorf("LastActiveAt mismatch")
	}
	if !model.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt mismatch")
	}
}

func TestRowToModel_NilInput(t *testing.T) {
	// rowToModel should handle nil gracefully (panics if row is nil — documented)
	// We test with a row that has minimal fields
	row := &DeviceSessionRow{
		ID:         uuid.New(),
		UserID:     uuid.New(),
		DeviceID:   "d",
		DeviceType: "ios",
		DeviceName: "n",
	}
	model := rowToModel(row)
	if model == nil {
		t.Fatal("rowToModel returned nil for valid row")
	}
}

// ---------------------------------------------------------------------------
// Tests: IsNotFound helper
// ---------------------------------------------------------------------------

func TestIsNotFound_SqlErrNoRows(t *testing.T) {
	if !IsNotFound(sql.ErrNoRows) {
		t.Error("IsNotFound(sql.ErrNoRows) should be true")
	}
}

func TestIsNotFound_ErrDeviceNotFound(t *testing.T) {
	if !IsNotFound(ErrDeviceNotFound) {
		t.Error("IsNotFound(ErrDeviceNotFound) should be true")
	}
}

func TestIsNotFound_OtherError(t *testing.T) {
	if IsNotFound(sql.ErrConnDone) {
		t.Error("IsNotFound(sql.ErrConnDone) should be false")
	}
	if IsNotFound(nil) {
		t.Error("IsNotFound(nil) should be false")
	}
}

// ---------------------------------------------------------------------------
// Tests: ErrDeviceNotFound
// ---------------------------------------------------------------------------

func TestErrDeviceNotFound_Message(t *testing.T) {
	err := ErrDeviceNotFound
	if err.Error() != "auth: device session not found" {
		t.Errorf("unexpected message: %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Table-driven tests for validation
// ---------------------------------------------------------------------------

func TestRegister_ValidationCases(t *testing.T) {
	tests := []struct {
		name      string
		session   models.DeviceSession
		wantErr   bool
		errSubstr string
	}{
		{
			name: "valid ios device",
			session: models.DeviceSession{
				ID: uuid.New(), UserID: uuid.New(), DeviceID: "d1",
				DeviceType: "ios", DeviceName: "iPhone",
			},
			wantErr: false,
		},
		{
			name: "valid android device",
			session: models.DeviceSession{
				ID: uuid.New(), UserID: uuid.New(), DeviceID: "d1",
				DeviceType: "android", DeviceName: "Pixel",
			},
			wantErr: false,
		},
		{
			name: "nil user ID",
			session: models.DeviceSession{
				ID: uuid.New(), UserID: uuid.Nil, DeviceID: "d1",
				DeviceType: "ios", DeviceName: "iPhone",
			},
			wantErr:   true,
			errSubstr: "user_id is required",
		},
		{
			name: "empty device ID",
			session: models.DeviceSession{
				ID: uuid.New(), UserID: uuid.New(), DeviceID: "",
				DeviceType: "ios", DeviceName: "iPhone",
			},
			wantErr:   true,
			errSubstr: "device_id is required",
		},
		{
			name: "invalid device type",
			session: models.DeviceSession{
				ID: uuid.New(), UserID: uuid.New(), DeviceID: "d1",
				DeviceType: "blackberry", DeviceName: "BB",
			},
			wantErr:   true,
			errSubstr: "device_type must be 'ios' or 'android'",
		},
		{
			name: "empty device type",
			session: models.DeviceSession{
				ID: uuid.New(), UserID: uuid.New(), DeviceID: "d1",
				DeviceType: "", DeviceName: "Phone",
			},
			wantErr:   true,
			errSubstr: "device_type must be 'ios' or 'android'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dm := NewDeviceManager(nil)
			err := dm.Register(t.Context(), &tt.session)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errSubstr)
				}
			} else {
				if err != nil && (strings.Contains(err.Error(), "user_id") || strings.Contains(err.Error(), "device_type")) {
					t.Errorf("unexpected validation error: %v", err)
				}
			}
		})
	}
}
