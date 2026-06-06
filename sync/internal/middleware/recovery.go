// Package middleware provides HTTP middleware for the sync service.
package middleware

import (
	"fmt"
	"net/http"

	"github.com/decisionstack/sync/internal/logger"
)

// Recovery catches panics in HTTP handlers and returns a 500 error.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("http handler panic recovered",
					"panic", fmt.Sprintf("%+v", rec),
					"method", r.Method,
					"path", r.URL.Path,
					"request_id", r.Header.Get("X-Request-ID"),
				)

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"code":"internal_error","message":"An internal error occurred","retry":true}`+"\n")
			}
		}()

		next.ServeHTTP(w, r)
	})
}
