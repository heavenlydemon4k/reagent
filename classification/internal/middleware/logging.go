package middleware

import (
	"net/http"
	"time"

	"github.com/decisionstack/classification/internal/logger"
	"github.com/go-chi/chi/v5/middleware"
)

// StructuredLogger is a chi-compatible middleware that logs requests via slog.
type StructuredLogger struct {
	Log *logger.Logger
}

// NewLogging creates logging middleware wrapping the application logger.
func NewLogging(log *logger.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			defer func() {
				reqID := GetRequestID(r.Context())
				log := log.With(
					"request_id", reqID,
					"method", r.Method,
					"path", r.URL.Path,
					"remote_addr", r.RemoteAddr,
					"user_agent", r.UserAgent(),
					"status", ww.Status(),
					"bytes_written", ww.BytesWritten(),
					"duration_ms", time.Since(start).Milliseconds(),
				)

				if ww.Status() >= 500 {
					log.Error("request completed")
				} else if ww.Status() >= 400 {
					log.Warn("request completed")
				} else {
					log.Debug("request completed")
				}
			}()

			next.ServeHTTP(ww, r)
		})
	}
}
