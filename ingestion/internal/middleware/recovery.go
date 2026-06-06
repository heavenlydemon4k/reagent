// Package middleware provides HTTP middleware for the Ingestion Mesh.
package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/decisionstack/ingestion/internal/logger"
)

// Recovery middleware recovers from panics and returns a 500 error.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				ctx := r.Context()
				logger.Error(ctx, "panic recovered",
					"error", fmt.Sprintf("%v", rec),
					"stack", string(debug.Stack()),
					"method", r.Method,
					"path", r.URL.Path,
				)

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"internal server error"}`))
			}
		}()

		next.ServeHTTP(w, r)
	})
}
