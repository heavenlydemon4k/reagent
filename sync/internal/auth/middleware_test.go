// Package auth_test provides unit tests for JWT middleware.
package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Helper: make authenticated request
// ---------------------------------------------------------------------------

func mustMakeToken(t *testing.T, tm *TokenManager, userID uuid.UUID, deviceID string) string {
	t.Helper()
	token, err := tm.GenerateAccessToken(userID, deviceID)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	return token
}

func mustMakeRequest(t *testing.T, method, path, authHeader string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	return req
}

// ---------------------------------------------------------------------------
// Tests: JWTMiddleware
// ---------------------------------------------------------------------------

func TestJWTMiddleware_ValidToken(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	token := mustMakeToken(t, tm, testUserID, testDeviceID)

	var capturedUserID uuid.UUID
	var capturedDeviceID string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedUserID = UserIDFromContext(r.Context())
		capturedDeviceID = DeviceIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	middleware := JWTMiddleware(tm)
	wrapped := middleware(handler)

	req := mustMakeRequest(t, "GET", "/api/test", "Bearer "+token)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: want %d, got %d, body: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
	if capturedUserID != testUserID {
		t.Errorf("userID: want %s, got %s", testUserID, capturedUserID)
	}
	if capturedDeviceID != testDeviceID {
		t.Errorf("deviceID: want %q, got %q", testDeviceID, capturedDeviceID)
	}
}

func TestJWTMiddleware_MissingAuthHeader(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := JWTMiddleware(tm)
	wrapped := middleware(handler)

	req := mustMakeRequest(t, "GET", "/api/test", "")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: want %d, got %d", http.StatusUnauthorized, rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "auth_missing") {
		t.Errorf("body should contain auth_missing, got: %s", body)
	}
}

func TestJWTMiddleware_MalformedHeader_NoBearerPrefix(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	token := mustMakeToken(t, tm, testUserID, testDeviceID)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := JWTMiddleware(tm)
	wrapped := middleware(handler)

	req := mustMakeRequest(t, "GET", "/api/test", "Basic "+token)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: want %d, got %d", http.StatusUnauthorized, rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "auth_malformed") {
		t.Errorf("body should contain auth_malformed, got: %s", body)
	}
}

func TestJWTMiddleware_EmptyBearerToken(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := JWTMiddleware(tm)
	wrapped := middleware(handler)

	req := mustMakeRequest(t, "GET", "/api/test", "Bearer ")
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: want %d, got %d", http.StatusUnauthorized, rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "auth_empty") {
		t.Errorf("body should contain auth_empty, got: %s", body)
	}
}

func TestJWTMiddleware_ExpiredToken(t *testing.T) {
	// Create a token manager that generates already-expired tokens
	tm := NewTokenManager(testSecret, -time.Hour, testLongTTL)
	token := mustMakeToken(t, tm, testUserID, testDeviceID)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := JWTMiddleware(tm)
	wrapped := middleware(handler)

	req := mustMakeRequest(t, "GET", "/api/test", "Bearer "+token)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: want %d, got %d", http.StatusUnauthorized, rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "auth_expired") {
		t.Errorf("body should contain auth_expired, got: %s", body)
	}
}

func TestJWTMiddleware_InvalidSignature(t *testing.T) {
	tm1 := NewTokenManager([]byte("secret-one"), testShortTTL, testLongTTL)
	token := mustMakeToken(t, tm1, testUserID, testDeviceID)

	tm2 := NewTokenManager([]byte("secret-two"), testShortTTL, testLongTTL)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := JWTMiddleware(tm2)
	wrapped := middleware(handler)

	req := mustMakeRequest(t, "GET", "/api/test", "Bearer "+token)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: want %d, got %d", http.StatusUnauthorized, rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "auth_invalid") {
		t.Errorf("body should contain auth_invalid, got: %s", body)
	}
}

func TestJWTMiddleware_TamperedToken(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	token := mustMakeToken(t, tm, testUserID, testDeviceID)
	tampered := token[:len(token)-10] + "TAMPERED!!"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := JWTMiddleware(tm)
	wrapped := middleware(handler)

	req := mustMakeRequest(t, "GET", "/api/test", "Bearer "+tampered)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: want %d, got %d", http.StatusUnauthorized, rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Tests: Context extraction helpers
// ---------------------------------------------------------------------------

func TestUserIDFromContext_Present(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxKeyUserID, testUserID)
	got := UserIDFromContext(ctx)
	if got != testUserID {
		t.Errorf("want %s, got %s", testUserID, got)
	}
}

func TestUserIDFromContext_Missing(t *testing.T) {
	ctx := context.Background()
	got := UserIDFromContext(ctx)
	if got != uuid.Nil {
		t.Errorf("want uuid.Nil, got %s", got)
	}
}

func TestDeviceIDFromContext_Present(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxKeyDeviceID, "device-123")
	got := DeviceIDFromContext(ctx)
	if got != "device-123" {
		t.Errorf("want 'device-123', got %q", got)
	}
}

func TestDeviceIDFromContext_Missing(t *testing.T) {
	ctx := context.Background()
	got := DeviceIDFromContext(ctx)
	if got != "" {
		t.Errorf("want empty string, got %q", got)
	}
}

func TestMustGetUserID_Success(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxKeyUserID, testUserID)
	uid, err := MustGetUserID(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != testUserID {
		t.Errorf("want %s, got %s", testUserID, uid)
	}
}

func TestMustGetUserID_Missing(t *testing.T) {
	ctx := context.Background()
	_, err := MustGetUserID(ctx)
	if err == nil {
		t.Fatal("expected error for missing user ID")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestMustGetUserID_NilUUID(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxKeyUserID, uuid.Nil)
	_, err := MustGetUserID(ctx)
	if err == nil {
		t.Fatal("expected error for nil UUID")
	}
}

// ---------------------------------------------------------------------------
// Tests: AuthRoutes mounting
// ---------------------------------------------------------------------------

func TestAuthRoutes_MountsCorrectPaths(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)
	dm := NewDeviceManager(nil) // nil db is OK for route testing
	h := NewHandler(tm, dm)

	r := chi.NewRouter()
	AuthRoutes(r, h)

	// Verify routes are registered by checking the chi router
	routes := make(map[string]bool)
	walk := func(method, route string, handler http.Handler, middlewares ...func(http.Handler) http.Handler) error {
		routes[method+" "+route] = true
		return nil
	}
	err := chi.Walk(r, walk)
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}

	expected := []string{
		"POST /auth/device",
		"POST /auth/refresh",
		"POST /auth/revoke",
		"GET /auth/sessions",
	}
	for _, exp := range expected {
		if !routes[exp] {
			t.Errorf("expected route %s not found", exp)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: writeJSONError helper
// ---------------------------------------------------------------------------

func TestWriteJSONError(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSONError(rr, http.StatusUnauthorized, "test_code", "test message")

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: want %d, got %d", http.StatusUnauthorized, rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("content-type: want application/json, got %q", ct)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "test_code") {
		t.Errorf("body should contain error code, got: %s", body)
	}
	if !strings.Contains(body, "test message") {
		t.Errorf("body should contain error message, got: %s", body)
	}
}

// ---------------------------------------------------------------------------
// Tests: ProtectedRoutes
// ---------------------------------------------------------------------------

func TestProtectedRoutes_AppliesMiddleware(t *testing.T) {
	tm := NewTokenManager(testSecret, testShortTTL, testLongTTL)

	router := chi.NewRouter()
	protected := ProtectedRoutes(tm)
	router.Group(func(r chi.Router) {
		r.Use(protected(r))
		r.Get("/protected", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("access granted"))
		})
	})

	// Without token → 401
	req := httptest.NewRequest("GET", "/protected", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("without token: want %d, got %d", http.StatusUnauthorized, rr.Code)
	}

	// With valid token → 200
	token := mustMakeToken(t, tm, testUserID, testDeviceID)
	req = httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("with token: want %d, got %d, body: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
}
