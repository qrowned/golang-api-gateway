package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// RedisChecker is satisfied by redis.Client (Ping method).
type RedisChecker interface {
	Ping(ctx context.Context) error
}

type Handler struct {
	redis RedisChecker
}

// NewHandler creates health check handlers. redis may be nil (readyz will report degraded).
func NewHandler(redis RedisChecker) *Handler {
	return &Handler{redis: redis}
}

// Liveness is GET /healthz — always 200 while the process is alive.
func (h *Handler) Liveness(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// Readiness is GET /readyz — 200 if all dependencies are ready, 503 otherwise.
func (h *Handler) Readiness(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if h.redis == nil {
		writeReady(w, "redis", "no redis client configured")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := h.redis.Ping(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "degraded",
			"reason": "redis ping failed: " + err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func writeReady(w http.ResponseWriter, component, reason string) {
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":    "degraded",
		"component": component,
		"reason":    reason,
	})
}
