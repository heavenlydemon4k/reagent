package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/decisionstack/classification/internal/logger"
)

// Recovery recovers from panics and returns 500.
func Recovery(log *logger.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					reqID := GetRequestID(r.Context())
					log.Error("panic recovered",
						"request_id", reqID,
						"panic", fmt.Sprintf("%+v", rec),
						"stack", string(debug.Stack()),
					)
					http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
