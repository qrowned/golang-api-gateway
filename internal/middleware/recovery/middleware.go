package recovery

import (
	"encoding/json"
	"net/http"
	"runtime/debug"

	"golang-api-gateway/pkg/logger"
)

// Middleware recovers from panics, logs a stack trace, and returns a 500.
// Stack traces are never leaked to the client.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log := logger.FromContext(r.Context())
				log.Error("panic recovered",
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "internal server error",
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
