package ratelimit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lucabartmann/golang-api-gateway/internal/config"
	"github.com/lucabartmann/golang-api-gateway/pkg/logger"
)

// Middleware enforces the given rate limit profile, returning 429 with Retry-After on exceeded limits.
func Middleware(limiter Limiter, profile config.RateLimitProfile) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := buildKey(r, profile.KeyStrategy)
			limit := profile.RPS
			window := profile.WindowSize
			if window == 0 {
				window = time.Second
			}

			ctx := r.Context()
			allowed, remaining, retryAfter, err := limiter.Allow(ctx, key, limit, window)
			if err != nil {
				log := logger.FromContext(ctx)
				log.Error("rate limiter error", "error", err, "profile", profile.ID)
				// Fail open on limiter errors to avoid cascading failures
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
			w.Header().Set("X-RateLimit-Profile", profile.ID)
			if !allowed {
				resetTime := time.Now().Add(retryAfter)
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetTime.Unix(), 10))
				w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":       "rate limit exceeded",
					"retry_after": strconv.Itoa(int(retryAfter.Seconds()) + 1),
				})
				return
			}
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))

			next.ServeHTTP(w, r)
		})
	}
}

func buildKey(r *http.Request, strategy string) string {
	switch strategy {
	case "user":
		if uid := r.Header.Get("X-User-ID"); uid != "" {
			return fmt.Sprintf("ratelimit:user:%s:%s", uid, r.URL.Path)
		}
		fallthrough
	case "api_key":
		if key := r.Header.Get("X-API-Key"); key != "" {
			return fmt.Sprintf("ratelimit:apikey:%s:%s", key, r.URL.Path)
		}
		fallthrough
	default: // "ip"
		ip := clientIP(r)
		return fmt.Sprintf("ratelimit:ip:%s:%s", ip, r.URL.Path)
	}
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	return host
}
