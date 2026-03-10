package logging

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"golang-api-gateway/pkg/logger"
)

// Middleware injects a request-scoped logger and logs every request.
// It also sets X-Request-ID (via chi's RequestID middleware convention).
func Middleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			reqID := middleware.GetReqID(r.Context())

			// Enrich logger with request metadata
			reqLogger := log.With(
				"request_id", reqID,
				"method", r.Method,
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
			)

			ctx := logger.WithContext(r.Context(), reqLogger)
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r.WithContext(ctx))

			reqLogger.Info("request completed",
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}
