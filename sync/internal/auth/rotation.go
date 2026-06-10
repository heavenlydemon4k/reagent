// ==============================================================================
// Package auth — JWT Key Rotation with 24h Grace Period
// ==============================================================================
//
// Grace Period Rotation Protocol:
//
//   Phase 0: Normal Operation
//     Validator holds: { current_kid: secret_a }
//
//   Phase 1: Rotation Initiated
//     1. Generate new signing key (secret_b)
//     2. Update validator: { current_kid: secret_b, previous_kid: secret_a }
//     3. Set grace_period_end = now + 24h
//     4. New tokens signed with kid_B
//     5. Old tokens (kid_A) still validate during grace period
//
//   Phase 2: Grace Period Active (0-24h)
//     Validator accepts both kid_A and kid_B
//     All existing sessions continue working
//
//   Phase 3: Grace Period Ends (after 24h)
//     1. Remove previous key: { current_kid: secret_b }
//     2. Tokens signed with kid_A now fail validation
//     3. Users with old tokens must re-authenticate
//
//   Recovery: If rotation fails at any point, call Rollback() to restore
//   the previous key as current, discarding the new key.
//
// ==============================================================================

package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

// ------------------------------------------------------------------------------
// Default Configuration
// ------------------------------------------------------------------------------

// DefaultGracePeriod is the duration old keys remain valid after rotation.
const DefaultGracePeriod = 24 * time.Hour

// SigningKeyLength is the byte length of HS256 signing keys (512 bits).
const SigningKeyLength = 64

// ------------------------------------------------------------------------------
// RotationState tracks the current rotation phase
// ------------------------------------------------------------------------------

// RotationState represents the current key rotation phase.
type RotationState int

const (
	// RotationIdle means no rotation is in progress.
	RotationIdle RotationState = iota
	// RotationGracePeriod means a new key is active and old key is in grace period.
	RotationGracePeriod
)

func (s RotationState) String() string {
	switch s {
	case RotationIdle:
		return "idle"
	case RotationGracePeriod:
		return "grace_period"
	default:
		return "unknown"
	}
}

// ------------------------------------------------------------------------------
// RotationRecord — stored in Secrets Manager for persistence
// ------------------------------------------------------------------------------

// RotationRecord is the JSON structure stored in Secrets Manager.
type RotationRecord struct {
	CurrentKey      string    `json:"current_key"`       // hex-encoded
	PreviousKey     string    `json:"previous_key"`      // hex-encoded, empty if none
	CurrentKID      string    `json:"current_kid"`
	PreviousKID     string    `json:"previous_kid"`
	RotatedAt       time.Time `json:"rotated_at"`
	GracePeriodEnd  time.Time `json:"grace_period_end"`
	RotationState   string    `json:"rotation_state"`
	Version         int       `json:"version"`
}

// ------------------------------------------------------------------------------
// KeyRotator — manages the full rotation lifecycle
// ------------------------------------------------------------------------------

// KeyRotator manages JWT signing key rotation with grace period support.
type KeyRotator struct {
	mu       sync.RWMutex
	validator *MultiKeyValidator
	record   RotationRecord

	// Configuration
	gracePeriod time.Duration

	// AWS clients
	secretsClient *secretsmanager.Client
	secretARN     string // ARN of the jwt-signing-key secret in Secrets Manager

	// Internal state
	rotationTimer *time.Timer
	onRotated     func(oldKID, newKID string) // optional callback
}

// RotatorOption configures a KeyRotator.
type RotatorOption func(*KeyRotator)

// WithGracePeriod sets a custom grace period duration (default: 24h).
func WithGracePeriod(d time.Duration) RotatorOption {
	return func(kr *KeyRotator) {
		kr.gracePeriod = d
	}
}

// WithRotationCallback sets a callback invoked on rotation events.
func WithRotationCallback(fn func(oldKID, newKID string)) RotatorOption {
	return func(kr *KeyRotator) {
		kr.onRotated = fn
	}
}

// NewKeyRotator creates a new KeyRotator that manages a MultiKeyValidator.
// The initialKey is the current signing key loaded from Secrets Manager.
func NewKeyRotator(initialKey []byte, secretsClient *secretsmanager.Client, secretARN string, opts ...RotatorOption) *KeyRotator {
	kr := &KeyRotator{
		validator:     NewMultiKeyValidator(initialKey),
		gracePeriod:   DefaultGracePeriod,
		secretsClient: secretsClient,
		secretARN:     secretARN,
		record: RotationRecord{
			CurrentKey:    hex.EncodeToString(initialKey),
			CurrentKID:    deriveKID(initialKey),
			RotationState: RotationIdle.String(),
			Version:       1,
		},
	}

	for _, opt := range opts {
		opt(kr)
	}

	return kr
}

// Validator returns the underlying MultiKeyValidator for token operations.
func (kr *KeyRotator) Validator() *MultiKeyValidator {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	return kr.validator
}

// ------------------------------------------------------------------------------
// Rotation Operations
// ------------------------------------------------------------------------------

// Rotate initiates a new key rotation:
//  1. Generates a new random signing key
//  2. Stores it as "current" in the validator
//  3. Keeps the old key during the grace period
//  4. Persists state to Secrets Manager
//  5. Schedules cleanup after grace period
func (kr *KeyRotator) Rotate() (newKID string, err error) {
	kr.mu.Lock()
	defer kr.mu.Unlock()

	// Generate new signing key
	newKey := make([]byte, SigningKeyLength)
	if _, err := rand.Read(newKey); err != nil {
		return "", fmt.Errorf("generate new signing key: %w", err)
	}

	// Derive kids
	oldKID := kr.validator.currentID
	newKID = deriveKID(newKey)

	// Prevent rotating to same key (impossible with 512-bit random, but be safe)
	if newKID == oldKID {
		return "", fmt.Errorf("new key collision (statistically impossible) — retry")
	}

	// If there's already a previous key from an incomplete rotation, remove it
	if kr.validator.previousID != "" {
		delete(kr.validator.keys, kr.validator.previousID)
	}

	// Move current to previous, add new as current
	kr.validator.previousID = oldKID
	kr.validator.keys[newKID] = newKey
	kr.validator.currentID = newKID

	graceEnd := time.Now().UTC().Add(kr.gracePeriod)
	kr.validator.gracePeriodEnd = graceEnd
	kr.validator.gracePeriodActive = true

	// Update record
	oldKey := kr.validator.keys[oldKID]
	kr.record = RotationRecord{
		CurrentKey:       hex.EncodeToString(newKey),
		PreviousKey:      hex.EncodeToString(oldKey),
		CurrentKID:       newKID,
		PreviousKID:      oldKID,
		RotatedAt:        time.Now().UTC(),
		GracePeriodEnd:   graceEnd,
		RotationState:    RotationGracePeriod.String(),
		Version:          kr.record.Version + 1,
	}

	// Persist to Secrets Manager
	if err := kr.persistRecordLocked(); err != nil {
		// Attempt rollback on persistence failure
		kr.rollbackLocked()
		return "", fmt.Errorf("persist rotation record: %w", err)
	}

	// Schedule grace period cleanup
	kr.scheduleCleanupLocked(graceEnd)

	// Invoke callback
	if kr.onRotated != nil {
		go kr.onRotated(oldKID, newKID)
	}

	return newKID, nil
}

// Rollback cancels an in-progress rotation and restores the previous key
// as current. This is a recovery mechanism for failed rotations.
func (kr *KeyRotator) Rollback() error {
	kr.mu.Lock()
	defer kr.mu.Unlock()

	return kr.rollbackLocked()
}

func (kr *KeyRotator) rollbackLocked() error {
	if !kr.validator.gracePeriodActive || kr.validator.previousID == "" {
		return fmt.Errorf("no rotation in progress to rollback")
	}

	// Remove new (current) key
	newKID := kr.validator.currentID
	delete(kr.validator.keys, newKID)

	// Restore previous as current
	previousKID := kr.validator.previousID
	kr.validator.currentID = previousKID
	kr.validator.previousID = ""
	kr.validator.gracePeriodActive = false
	kr.validator.gracePeriodEnd = time.Time{}

	// Cancel cleanup timer
	if kr.rotationTimer != nil {
		kr.rotationTimer.Stop()
		kr.rotationTimer = nil
	}

	// Update record
	kr.record.RotationState = RotationIdle.String()
	kr.record.PreviousKey = ""
	kr.record.PreviousKID = ""

	// Persist
	if err := kr.persistRecordLocked(); err != nil {
		return fmt.Errorf("persist rollback: %w", err)
	}

	return nil
}

// CompleteGracePeriod ends the grace period early (manual override).
// After calling this, old tokens will immediately fail validation.
func (kr *KeyRotator) CompleteGracePeriod() error {
	kr.mu.Lock()
	defer kr.mu.Unlock()

	return kr.completeGracePeriodLocked()
}

func (kr *KeyRotator) completeGracePeriodLocked() error {
	if !kr.validator.gracePeriodActive {
		return nil // Already complete
	}

	previousKID := kr.validator.previousID

	// Remove previous key
	if previousKID != "" {
		delete(kr.validator.keys, previousKID)
	}

	kr.validator.previousID = ""
	kr.validator.gracePeriodActive = false
	kr.validator.gracePeriodEnd = time.Time{}

	// Update record
	kr.record.PreviousKey = ""
	kr.record.PreviousKID = ""
	kr.record.RotationState = RotationIdle.String()

	// Cancel timer
	if kr.rotationTimer != nil {
		kr.rotationTimer.Stop()
		kr.rotationTimer = nil
	}

	return kr.persistRecordLocked()
}

// ------------------------------------------------------------------------------
// Scheduled Cleanup
// ------------------------------------------------------------------------------

// scheduleCleanupLocked schedules the grace period cleanup.
// Must be called with kr.mu held.
func (kr *KeyRotator) scheduleCleanupLocked(endTime time.Time) {
	// Cancel existing timer
	if kr.rotationTimer != nil {
		kr.rotationTimer.Stop()
	}

	delay := time.Until(endTime)
	if delay < 0 {
		delay = 0
	}

	kr.rotationTimer = time.AfterFunc(delay, func() {
		kr.mu.Lock()
		defer kr.mu.Unlock()

		// Double-check grace period is still active
		if !kr.validator.gracePeriodActive {
			return
		}

		// Emit callback before completing
		if kr.onRotated != nil {
			kr.onRotated(kr.validator.previousID, "") // old key removed
		}

		kr.completeGracePeriodLocked()
	})
}

// ------------------------------------------------------------------------------
// Persistence — Secrets Manager
// ------------------------------------------------------------------------------

// persistRecordLocked saves the rotation state to Secrets Manager.
// Must be called with kr.mu held.
func (kr *KeyRotator) persistRecordLocked() error {
	if kr.secretsClient == nil || kr.secretARN == "" {
		return nil // No persistence configured — local/dev mode
	}

	recordJSON, err := json.Marshal(kr.record)
	if err != nil {
		return fmt.Errorf("marshal rotation record: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = kr.secretsClient.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId:     aws.String(kr.secretARN),
		SecretString: aws.String(string(recordJSON)),
		VersionStages: []string{
			"AWSCURRENT",
		},
	})
	if err != nil {
		// Handle ResourceExistsException by updating instead
		var resourceExists *types.ResourceExistsException
		if errors.As(err, &resourceExists) {
			_, err = kr.secretsClient.UpdateSecret(ctx, &secretsmanager.UpdateSecretInput{
				SecretId:     aws.String(kr.secretARN),
				SecretString: aws.String(string(recordJSON)),
			})
		}
		if err != nil {
			return fmt.Errorf("put secret value: %w", err)
		}
	}

	return nil
}

// LoadFromSecretManager loads key state from a Secrets Manager secret.
// This is called on startup to restore rotation state after a restart.
func LoadFromSecretManager(secretsClient *secretsmanager.Client, secretARN string, opts ...RotatorOption) (*KeyRotator, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := secretsClient.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretARN),
	})
	if err != nil {
		return nil, fmt.Errorf("get secret value: %w", err)
	}

	var record RotationRecord
	if err := json.Unmarshal([]byte(*result.SecretString), &record); err != nil {
		return nil, fmt.Errorf("unmarshal rotation record: %w", err)
	}

	// Decode current key
	currentKey, err := hex.DecodeString(record.CurrentKey)
	if err != nil {
		return nil, fmt.Errorf("decode current key: %w", err)
	}

	// Create validator with current key
	kr := NewKeyRotator(currentKey, secretsClient, secretARN, opts...)

	// If a rotation was in progress, restore the previous key too
	if record.RotationState == RotationGracePeriod.String() && record.PreviousKey != "" {
		previousKey, err := hex.DecodeString(record.PreviousKey)
		if err != nil {
			return nil, fmt.Errorf("decode previous key: %w", err)
		}

		previousKID := deriveKID(previousKey)
		kr.validator.keys[previousKID] = previousKey
		kr.validator.previousID = previousKID
		kr.validator.gracePeriodActive = true
		kr.validator.gracePeriodEnd = record.GracePeriodEnd

		// If grace period hasn't expired yet, schedule cleanup
		if time.Now().UTC().Before(record.GracePeriodEnd) {
			kr.scheduleCleanupLocked(record.GracePeriodEnd)
		} else {
			// Grace period already expired — clean up now
			kr.completeGracePeriodLocked()
		}
	}

	kr.record = record
	return kr, nil
}

// ------------------------------------------------------------------------------
// Health & Status
// ------------------------------------------------------------------------------

// RotationStatus provides a snapshot of the current rotation state.
type RotationStatus struct {
	State                string        `json:"state"`
	CurrentKID           string        `json:"current_kid"`
	PreviousKID          string        `json:"previous_kid,omitempty"`
	KeyCount             int           `json:"key_count"`
	GracePeriodActive    bool          `json:"grace_period_active"`
	GracePeriodEnd       *time.Time    `json:"grace_period_end,omitempty"`
	GracePeriodRemaining time.Duration `json:"grace_period_remaining,omitempty"`
	LastRotatedAt        *time.Time    `json:"last_rotated_at,omitempty"`
	Version              int           `json:"version"`
}

// Status returns the current rotation status.
func (kr *KeyRotator) Status() RotationStatus {
	kr.mu.RLock()
	defer kr.mu.RUnlock()

	s := RotationStatus{
		State:      kr.record.RotationState,
		CurrentKID: kr.validator.currentID,
		KeyCount:   len(kr.validator.keys),
		Version:    kr.record.Version,
	}

	if kr.validator.gracePeriodActive {
		s.GracePeriodActive = true
		s.PreviousKID = kr.validator.previousID
		s.GracePeriodEnd = &kr.validator.gracePeriodEnd
		remaining := time.Until(kr.validator.gracePeriodEnd)
		if remaining > 0 {
			s.GracePeriodRemaining = remaining
		}
	}

	if !kr.record.RotatedAt.IsZero() {
		s.LastRotatedAt = &kr.record.RotatedAt
	}

	return s
}

// ------------------------------------------------------------------------------
// Key Generation Utilities
// ------------------------------------------------------------------------------

// GenerateSigningKey creates a new cryptographically secure HS256 signing key.
func GenerateSigningKey() ([]byte, error) {
	key := make([]byte, SigningKeyLength)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate signing key: %w", err)
	}
	return key, nil
}

// GenerateSigningKeyHex creates a new signing key and returns it as hex string.
func GenerateSigningKeyHex() (string, string, error) {
	key, err := GenerateSigningKey()
	if err != nil {
		return "", "", err
	}
	return hex.EncodeToString(key), deriveKID(key), nil
}
