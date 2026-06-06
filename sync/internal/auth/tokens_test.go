// Package auth_test provides unit tests for JWT token management.
package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Test fixtures
// ---------------------------------------------------------------------------

var (
	testSecret     = []byte("test-secret-not-for-production")
	testUserID     = uuid.MustParse("a1b2c3d4-e5f6-7890-abcd-ef1234567890")
	testDeviceID   = "test-device-ios-001"
	testShortTTL   = time.Hour
	testLongTTL    = time.Hour * 24 * 30
)

// ---------------------------------------------------------------------------
// Helper assertions (standard library only)
// ---------------------------------------------------------------------------

func assertNoError(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", msg, err)
	}
}

func assertError(t *testing.T, err error, msg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error but got nil", msg)
	}
}

func assertEqualUUID(t *testing.T, want, got uuid.UUID, msg string) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %s, got %s", msg, want, got)
	}
}

func assertEqualString(t *testing.T, want, got, msg string) {
	t.Helper()
	if want != got {
		t.Errorf("%s: want %q, got %q", msg, want, got)
	}
}

func assertTrue(t *testing.T, cond bool, msg string) {
	t.Helper()
	if !cond {
		t.Errorf("%s: expected true but got false", msg)
	}
}

func assertNotEmpty(t *testing.T, s string, msg string) {
	t.Helper()
	if s == "" {
		t.Errorf("%s: expected non-empty string", msg)
	}
}

// ---------------------------------------------------------------------------
// Tests: TokenManager construction
// ---------------------------------------------------------------------------

func TestNewTokenManager_WithValidConfig(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	if tm == nil {
		t.Fatal("NewTokenManager returned nil")
	}
}

func TestNewTokenManager_EmptySecretPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty secret")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "secret must not be empty") {
			t.Fatalf("unexpected panic message: %v", r)
		}
	}()
	_ = NewTokenManager([]byte{}, testShortTTL, testLongTTL)
}

func TestNewTokenManager_DefaultTTLs(t *testing.T) {
	tm := NewTokenManager(testSecret, 0, 0)
	if tm == nil {
		t.Fatal("NewTokenManager returned nil")
	}
	// Defaults: accessTTL = 24h, refreshTTL = 30d
	if tm.accessTTL != 24*time.Hour {
		t.Errorf("default accessTTL: want 24h, got %v", tm.accessTTL)
	}
	if tm.refreshTTL != 30*24*time.Hour {
		t.Errorf("default refreshTTL: want 720h, got %v", tm.refreshTTL)
	}
}

// ---------------------------------------------------------------------------
// Tests: Access Token lifecycle
// ---------------------------------------------------------------------------

func TestGenerateAccessToken_Success(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	token, err := tm.GenerateAccessToken(testUserID, testDeviceID)
	assertNoError(t, err, "generate access token")
	assertNotEmpty(t, token, "token should not be empty")
}

func TestValidateAccessToken_Success(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	token, err := tm.GenerateAccessToken(testUserID, testDeviceID)
	assertNoError(t, err, "generate access token")

	uid, did, err := tm.ValidateAccessToken(token)
	assertNoError(t, err, "validate access token")
	assertEqualUUID(t, testUserID, uid, "user ID mismatch")
	assertEqualString(t, testDeviceID, did, "device ID mismatch")
}

func TestValidateAccessToken_InvalidSignature(t *testing.T) {
	tm1 := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	token, err := tm1.GenerateAccessToken(testUserID, testDeviceID)
	assertNoError(t, err, "generate access token")

	// Validate with different secret
	tm2 := NewTokenManager([]byte("wrong-secret"), testShortTTL, testLongTTL)
	_, _, err = tm2.ValidateAccessToken(token)
	assertError(t, err, "expected validation to fail with wrong secret")
}

func TestValidateAccessToken_ExpiredToken(t *testing.T) {
	// Create token with negative TTL (already expired)
	tm := NewTokenManager(testSecret, -time.Hour, testLongTTL)
	token, err := tm.GenerateAccessToken(testUserID, testDeviceID)
	assertNoError(t, err, "generate expired token")

	_, _, err = tm.ValidateAccessToken(token)
	assertError(t, err, "expected expired token to fail validation")
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("expected expired error, got: %v", err)
	}
}

func TestValidateAccessToken_TamperedToken(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	token, err := tm.GenerateAccessToken(testUserID, testDeviceID)
	assertNoError(t, err, "generate access token")

	// Tamper with the token payload
	tampered := token[:len(token)-10] + "TAMPERED!!"
	_, _, err = tm.ValidateAccessToken(tampered)
	assertError(t, err, "expected tampered token to fail")
}

func TestValidateAccessToken_MalformedToken(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	_, _, err := tm.ValidateAccessToken("not.a.jwt")
	assertError(t, err, "expected malformed token to fail")
}

func TestValidateAccessToken_WrongTokenUse(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	// Generate a refresh token
	refreshToken, err := tm.GenerateRefreshToken(testUserID, testDeviceID)
	assertNoError(t, err, "generate refresh token")

	// Try to validate refresh token as access token
	_, _, err = tm.ValidateAccessToken(refreshToken)
	assertError(t, err, "expected refresh token to fail access validation")
	if !strings.Contains(err.Error(), "not access") {
		t.Errorf("expected 'not access' error, got: %v", err)
	}
}

func TestValidateAccessToken_EmptyToken(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	_, _, err := tm.ValidateAccessToken("")
	assertError(t, err, "expected empty token to fail")
}

// ---------------------------------------------------------------------------
// Tests: Access Token claims verification
// ---------------------------------------------------------------------------

func TestAccessTokenClaims_ContainCorrectData(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	tokenStr, err := tm.GenerateAccessToken(testUserID, testDeviceID)
	assertNoError(t, err, "generate access token")

	// Parse without validation to inspect claims
	token, _, err := new(jwt.Parser).ParseUnverified(tokenStr, &SyncClaims{})
	assertNoError(t, err, "parse unverified token")

	claims, ok := token.Claims.(*SyncClaims)
	if !ok {
		t.Fatal("could not cast claims to SyncClaims")
	}

	assertEqualString(t, testUserID.String(), claims.Subject, "subject claim")
	assertEqualString(t, testDeviceID, claims.DeviceID, "device_id claim")
	assertEqualString(t, "access", claims.TokenUse, "token_use claim")

	if claims.ExpiresAt == nil {
		t.Fatal("expires_at claim is nil")
	}
	if claims.IssuedAt == nil {
		t.Fatal("issued_at claim is nil")
	}

	// Token should expire after issued-at + accessTTL
	wantExpiry := claims.IssuedAt.Add(testShortTTL)
	if !claims.ExpiresAt.Equal(wantExpiry) {
		t.Errorf("expiry: want %v, got %v", wantExpiry, claims.ExpiresAt.Time)
	}
}

// ---------------------------------------------------------------------------
// Tests: Refresh Token lifecycle
// ---------------------------------------------------------------------------

func TestGenerateRefreshToken_Success(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	token, err := tm.GenerateRefreshToken(testUserID, testDeviceID)
	assertNoError(t, err, "generate refresh token")
	assertNotEmpty(t, token, "refresh token should not be empty")

	// Refresh token format: envelope.opaque
	parts := strings.Split(token, ".")
	// JWT envelope has 3 parts (header.payload.signature), then opaque secret
	// So total dots = 3 (JWT) + 1 (separator) = at least 4 parts
	if len(parts) < 4 {
		t.Errorf("refresh token should have composite format with separator, got %d parts", len(parts))
	}
}

func TestValidateRefreshToken_Success(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	token, err := tm.GenerateRefreshToken(testUserID, testDeviceID)
	assertNoError(t, err, "generate refresh token")

	uid, did, err := tm.ValidateRefreshToken(token)
	assertNoError(t, err, "validate refresh token")
	assertEqualUUID(t, testUserID, uid, "user ID mismatch")
	assertEqualString(t, testDeviceID, did, "device ID mismatch")
}

func TestValidateRefreshToken_InvalidSignature(t *testing.T) {
	tm1 := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	token, err := tm1.GenerateRefreshToken(testUserID, testDeviceID)
	assertNoError(t, err, "generate refresh token")

	tm2 := NewTokenManager([]byte("wrong-secret"), testShortTTL, testLongTTL)
	_, _, err = tm2.ValidateRefreshToken(token)
	assertError(t, err, "expected validation to fail with wrong secret")
}

func TestValidateRefreshToken_ExpiredToken(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, -testLongTTL)
	token, err := tm.GenerateRefreshToken(testUserID, testDeviceID)
	assertNoError(t, err, "generate expired refresh token")

	_, _, err = tm.ValidateRefreshToken(token)
	assertError(t, err, "expected expired refresh token to fail")
}

func TestValidateRefreshToken_TamperedOpaquePart(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	token, err := tm.GenerateRefreshToken(testUserID, testDeviceID)
	assertNoError(t, err, "generate refresh token")

	// Tamper with the opaque portion (after the last dot)
	tampered := token + "x"
	_, _, err = tm.ValidateRefreshToken(tampered)
	assertError(t, err, "expected tampered refresh token to fail")
}

func TestValidateRefreshToken_WrongTokenUse(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	// Generate an access token
	accessToken, err := tm.GenerateAccessToken(testUserID, testDeviceID)
	assertNoError(t, err, "generate access token")

	// Try to validate access token as refresh token
	_, _, err = tm.ValidateRefreshToken(accessToken)
	assertError(t, err, "expected access token to fail refresh validation")
	if !strings.Contains(err.Error(), "not refresh") {
		t.Errorf("expected 'not refresh' error, got: %v", err)
	}
}

func TestValidateRefreshToken_MalformedNoSeparator(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	// A plain JWT without the composite separator
	claims := SyncClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: testUserID.String(),
		},
		TokenUse: "refresh",
	}
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, _ := jwtToken.SignedString(testSecret)

	_, _, err := tm.ValidateRefreshToken(tokenStr)
	assertError(t, err, "expected token without separator to fail")
	if !strings.Contains(err.Error(), "no separator") {
		t.Errorf("expected 'no separator' error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests: ExtractRefreshTokenSecret
// ---------------------------------------------------------------------------

func TestExtractRefreshTokenSecret_ExtractsOpaquePart(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	token, err := tm.GenerateRefreshToken(testUserID, testDeviceID)
	assertNoError(t, err, "generate refresh token")

	secret := ExtractRefreshTokenSecret(token)
	assertNotEmpty(t, secret, "extracted secret should not be empty")

	// The secret should be base64-encoded, URL-safe, no padding
	if strings.Contains(secret, ".") {
		t.Error("extracted secret should not contain dots")
	}
}

func TestExtractRefreshTokenSecret_NoSeparatorReturnsFull(t *testing.T) {
	// If there's no separator, the full string is returned
	input := "no-separator-here"
	result := ExtractRefreshTokenSecret(input)
	assertEqualString(t, input, result, "should return full string when no separator")
}

// ---------------------------------------------------------------------------
// Tests: Cross-manager isolation
// ---------------------------------------------------------------------------

func TestTokenManagers_WithDifferentSecrets_AreIsolated(t *testing.T) {
	tmA := NewTokenManager([]byte("secret-A"), testShortTTL, testLongTTL)
	tmB := NewTokenManager([]byte("secret-B"), testShortTTL, testLongTTL)

	tokenA, _ := tmA.GenerateAccessToken(testUserID, testDeviceID)
	tokenB, _ := tmB.GenerateAccessToken(testUserID, testDeviceID)

	// A's token should not validate with B
	_, _, err := tmB.ValidateAccessToken(tokenA)
	assertError(t, err, "A's token should not validate with B")

	// B's token should not validate with A
	_, _, err = tmA.ValidateAccessToken(tokenB)
	assertError(t, err, "B's token should not validate with A")
}

// ---------------------------------------------------------------------------
// Tests: Concurrent token generation (race detector test)
// ---------------------------------------------------------------------------

func TestGenerateAccessToken_Concurrent(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)

	// Generate multiple tokens concurrently
	const n = 50
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			uid := uuid.New()
			did := "device-concurrent"
			token, err := tm.GenerateAccessToken(uid, did)
			if err != nil {
				errCh <- err
				return
			}
			vuid, vdid, err := tm.ValidateAccessToken(token)
			if err != nil {
				errCh <- err
				return
			}
			if vuid != uid {
				t.Errorf("concurrent uid mismatch")
			}
			if vdid != did {
				t.Errorf("concurrent did mismatch")
			}
			errCh <- nil
		}(i)
	}

	for i := 0; i < n; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("concurrent token generation failed: %v", err)
		}
	}
}
