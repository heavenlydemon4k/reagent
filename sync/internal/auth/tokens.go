// ==============================================================================
// Package auth — JWT Token Management with kid (Key ID) Support
// ==============================================================================
//
// This package provides JWT token generation and validation with graceful
// key rotation support via the "kid" header claim.
//
// Key Rotation Flow:
//   1. Generate new signing key
//   2. Add to MultiKeyValidator as "current" (new tokens use this key)
//   3. Keep old key mapped by its kid for 24h grace period
//   4. After grace period, remove old key
//   5. All existing tokens signed with old key continue working during grace
//
// The kid is derived as: hex(sha256(secret)[0:4]) — an 8-char hex prefix
// that uniquely identifies the signing key without revealing the secret.
//
// ==============================================================================

package auth

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ------------------------------------------------------------------------------
// Claims
// ------------------------------------------------------------------------------

// Claims represents the JWT claims used by Decision Stack.
type Claims struct {
	UserID   string `json:"uid"`
	DeviceID string `json:"did,omitempty"`
	Role     string `json:"role,omitempty"`
	TeamID   string `json:"tid,omitempty"`

	jwt.RegisteredClaims
}

// ------------------------------------------------------------------------------
// Token Generation
// ------------------------------------------------------------------------------

// kidPrefixLen is the number of bytes from the SHA-256 hash to use as kid.
// 4 bytes = 8 hex characters — sufficient for key disambiguation.
const kidPrefixLen = 4

// deriveKID computes the key ID from a secret's SHA-256 prefix.
func deriveKID(secret []byte) string {
	hash := sha256.Sum256(secret)
	return fmt.Sprintf("%x", hash[:kidPrefixLen])
}

// GenerateToken creates a new JWT signed with the provided secret.
// The "kid" header is automatically set for rotation tracking.
func GenerateToken(userID, deviceID string, secret []byte, expiry time.Duration) (string, error) {
	now := time.Now().UTC()

	claims := Claims{
		UserID:   userID,
		DeviceID: deviceID,
		Role:     "user",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "decision-stack",
			Audience:  jwt.ClaimStrings{"decision-stack-api"},
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// Set kid header for key rotation tracking
	kid := deriveKID(secret)
	token.Header["kid"] = kid
	token.Header["typ"] = "JWT"
	token.Header["alg"] = "HS256"

	return token.SignedString(secret)
}

// GenerateTokenWithClaims creates a token with custom claims.
func GenerateTokenWithClaims(claims Claims, secret []byte) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	kid := deriveKID(secret)
	token.Header["kid"] = kid
	token.Header["typ"] = "JWT"
	token.Header["alg"] = "HS256"

	return token.SignedString(secret)
}

// RefreshToken extends a token's expiry while preserving its claims.
// The new token is signed with the current key.
func RefreshToken(tokenStr string, secret []byte, newExpiry time.Duration) (string, error) {
	// Parse without validation to extract claims
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(tokenStr, &Claims{})
	if err != nil {
		return "", fmt.Errorf("parse existing token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok {
		return "", fmt.Errorf("invalid claims type")
	}

	// Update timestamps
	now := time.Now().UTC()
	claims.IssuedAt = jwt.NewNumericDate(now)
	claims.ExpiresAt = jwt.NewNumericDate(now.Add(newExpiry))

	return GenerateTokenWithClaims(*claims, secret)
}

// ------------------------------------------------------------------------------
// Single-Key Validator (legacy/simple deployments)
// ------------------------------------------------------------------------------

// Validator validates JWT tokens signed with a single key.
type Validator struct {
	secret []byte
	kid    string
}

// NewValidator creates a single-key validator.
func NewValidator(secret []byte) *Validator {
	return &Validator{
		secret: secret,
		kid:    deriveKID(secret),
	}
}

// Validate parses and validates a JWT token against the configured secret.
func (v *Validator) Validate(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// Verify signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		// Verify kid matches our key
		if tokenKID, ok := token.Header["kid"].(string); ok {
			if tokenKID != v.kid {
				return nil, fmt.Errorf("key ID mismatch: got %s, expected %s", tokenKID, v.kid)
			}
		}
		// If no kid, still accept (backward compatibility)

		return v.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))

	if err != nil {
		return nil, fmt.Errorf("token validation failed: %w", err)
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token claims")
}

// KID returns the key ID of the current validator key.
func (v *Validator) KID() string {
	return v.kid
}

// ------------------------------------------------------------------------------
// Multi-Key Validator — Graceful Rotation Support
// ------------------------------------------------------------------------------

// MultiKeyValidator supports multiple keys simultaneously for zero-downtime
// key rotation. Keys are indexed by their kid (hash prefix).
type MultiKeyValidator struct {
	mu        sync.RWMutex
	keys      map[string][]byte // kid -> secret
	currentID string            // kid of the current (newest) key

	// Grace period tracking
	previousID       string    // kid of the previous key (in grace period)
	gracePeriodEnd   time.Time // when the grace period expires
	gracePeriodActive bool
}

// NewMultiKeyValidator creates a new multi-key validator with the given
// current signing key. Additional keys can be added via RotateKey.
func NewMultiKeyValidator(secret []byte) *MultiKeyValidator {
	kid := deriveKID(secret)
	return &MultiKeyValidator{
		keys: map[string][]byte{
			kid: secret,
		},
		currentID: kid,
	}
}

// Validate parses and validates a JWT token, looking up the key by kid.
// Falls back to the current key if no kid is present (backward compat).
func (v *MultiKeyValidator) Validate(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// Verify signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		v.mu.RLock()
		defer v.mu.RUnlock()

		// Look up key by kid
		kid, ok := token.Header["kid"].(string)
		if ok && kid != "" {
			if key, exists := v.keys[kid]; exists {
				return key, nil
			}
			return nil, fmt.Errorf("unknown key ID: %s", kid)
		}

		// Fallback: use current key (for tokens without kid — legacy)
		return v.keys[v.currentID], nil
	}, jwt.WithValidMethods([]string{"HS256"}))

	if err != nil {
		return nil, fmt.Errorf("token validation failed: %w", err)
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		// Check if token is using a key that's past its grace period
		if tokenKID, ok := token.Header["kid"].(string); ok {
			v.mu.RLock()
			isExpired := v.gracePeriodActive &&
				tokenKID == v.previousID &&
				time.Now().UTC().After(v.gracePeriodEnd)
			v.mu.RUnlock()

			if isExpired {
				return nil, fmt.Errorf("token signed with expired key (kid=%s), please re-authenticate", tokenKID)
			}
		}

		return claims, nil
	}

	return nil, fmt.Errorf("invalid token claims")
}

// CurrentKey returns the current signing key and its kid.
func (v *MultiKeyValidator) CurrentKey() ([]byte, string) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	return v.keys[v.currentID], v.currentID
}

// CurrentKID returns the kid of the current signing key.
func (v *MultiKeyValidator) CurrentKID() string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.currentID
}

// KeyCount returns the number of keys currently held (1 or 2 during rotation).
func (v *MultiKeyValidator) KeyCount() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.keys)
}

// IsGracePeriodActive returns true if a key rotation grace period is in effect.
func (v *MultiKeyValidator) IsGracePeriodActive() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if !v.gracePeriodActive {
		return false
	}
	return time.Now().UTC().Before(v.gracePeriodEnd)
}

// GracePeriodRemaining returns the remaining grace period duration.
// Returns 0 if no grace period is active.
func (v *MultiKeyValidator) GracePeriodRemaining() time.Duration {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if !v.gracePeriodActive {
		return 0
	}

	remaining := time.Until(v.gracePeriodEnd)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// GetKeyInfo returns metadata about all managed keys for health/debugging.
func (v *MultiKeyValidator) GetKeyInfo() map[string]interface{} {
	v.mu.RLock()
	defer v.mu.RUnlock()

	info := map[string]interface{}{
		"current_kid":         v.currentID,
		"key_count":           len(v.keys),
		"grace_period_active": v.gracePeriodActive,
	}

	if v.gracePeriodActive {
		info["previous_kid"] = v.previousID
		info["grace_period_end"] = v.gracePeriodEnd.Format(time.RFC3339)
		info["grace_period_remaining_seconds"] = time.Until(v.gracePeriodEnd).Seconds()
	}

	return info
}

// ------------------------------------------------------------------------------
// Parse Utilities
// ------------------------------------------------------------------------------

// ParseKID extracts the kid from a token without validating it.
func ParseKID(tokenStr string) (string, error) {
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(tokenStr, &Claims{})
	if err != nil {
		return "", fmt.Errorf("parse token: %w", err)
	}

	kid, ok := token.Header["kid"].(string)
	if !ok {
		return "", fmt.Errorf("token has no kid header")
	}

	return kid, nil
}

// TokenExpiry extracts the expiry time from a token without validating.
func TokenExpiry(tokenStr string) (time.Time, error) {
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(tokenStr, &Claims{})
	if err != nil {
		return time.Time{}, fmt.Errorf("parse token: %w", err)
	}

	if claims, ok := token.Claims.(*Claims); ok && claims.ExpiresAt != nil {
		return claims.ExpiresAt.Time, nil
	}

	return time.Time{}, fmt.Errorf("token has no expiry")
}

// TokenSubject extracts the subject (userID) from a token without validating.
func TokenSubject(tokenStr string) (string, error) {
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(tokenStr, &Claims{})
	if err != nil {
		return "", fmt.Errorf("parse token: %w", err)
	}

	if claims, ok := token.Claims.(*Claims); ok {
		return claims.Subject, nil
	}

	return "", fmt.Errorf("invalid claims")
}
