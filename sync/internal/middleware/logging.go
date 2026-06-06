// Package middleware provides HTTP middleware for the sync service.
package middleware

import (
	"net/http"
	"time"

	"github.com/decisionstack/sync/internal/logger"
)

// Logging middleware logs incoming HTTP requests and their responses.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Capture response status and size
		ww := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(ww, r)

		duration := time.Since(start)

		attrs := logger.RequestAttrs{
			RequestID:  r.Header.Get("X-Request-ID"),
			Method:     r.Method,
			Path:       r.URL.Path,
			StatusCode: ww.statusCode,
			DurationMs: duration.Milliseconds(),
		}

		// Try to get user/device from context
		if uid, ok := r.Context().Value("user_id").(string); ok {
			attrs.UserID = uid
		}
		if did, ok := r.Context().Value("device_id").(string); ok {
			attrs.DeviceID = did
		}

		log := logger.WithRequest(r.Context(), attrs)

		switch {
		case ww.statusCode >= 500:
			log.Error("http request")
		case ww.statusCode >= 400:
			log.Warn("http request")
		default:
			log.Info("http request")
		}
	})
}

// responseWriter wraps http.ResponseWriter to capture status code and response size.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    int64
}

// WriteHeader captures the status code before delegating.
func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Write captures the response size before delegating.
func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.written += int64(n)
	return n, err
}
