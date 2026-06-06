// Package middleware provides HTTP middleware for the Ingestion Mesh.
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/decisionstack/ingestion/internal/logger"
)

// requestIDKey is the context key for request ID.
type requestIDKey struct{}

const requestIDHeader = "X-Request-ID"

// RequestID middleware injects a request ID into the context and response headers.
// If the client provides an X-Request-ID header, it is preserved.
// Otherwise, a new random request ID is generated.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get(requestIDHeader)
		if reqID == "" {
			reqID = generateRequestID()
		}

		// Add to response header
		w.Header().Set(requestIDHeader, reqID)

		// Add to context using both key types for compatibility
		ctx := context.WithValue(r.Context(), requestIDKey{}, reqID)
		ctx = context.WithValue(ctx, "request_id", reqID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetRequestID retrieves the request ID from context.
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}

// generateRequestID generates a 16-byte hex-encoded random ID.
func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		logger.Warn(nil, "failed to generate random request ID, using fallback")
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
