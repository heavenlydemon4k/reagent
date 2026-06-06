// Package middleware provides HTTP middleware for the sync service.
package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"
)

const (
	// RequestIDHeader is the HTTP header for request correlation IDs.
	RequestIDHeader = "X-Request-ID"
)

// RequestID adds a unique request ID to each HTTP request if not already present.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get(RequestIDHeader)
		if requestID == "" {
			requestID = generateRequestID()
		}

		// Set the request ID on the response header
		w.Header().Set(RequestIDHeader, requestID)

		// Also set it on the request so downstream handlers can access it
		r.Header.Set(RequestIDHeader, requestID)

		next.ServeHTTP(w, r)
	})
}

// generateRequestID creates a unique request ID.
func generateRequestID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID
		return fmt.Sprintf("req-%d", timeNow())
	}
	return "req-" + hex.EncodeToString(b)
}

// timeNow returns current Unix nanoseconds (extracted for testability).
var timeNow = func() int64 {
	return time.Now().UnixNano()
}
