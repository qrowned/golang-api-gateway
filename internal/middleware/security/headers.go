package security

import (
	"fmt"
	"net/http"

	"golang-api-gateway/internal/config"
)

// HeadersMiddleware sets security-related HTTP response headers on every response.
func HeadersMiddleware(cfg config.SecurityConfig) func(http.Handler) http.Handler {
	hsts := fmt.Sprintf("max-age=%d; includeSubDomains", cfg.HSTSMaxAge)
	frameOptions := cfg.FrameOptions
	if frameOptions == "" {
		frameOptions = "DENY"
	}
	referrerPolicy := cfg.ReferrerPolicy
	if referrerPolicy == "" {
		referrerPolicy = "strict-origin-when-cross-origin"
	}
	csp := cfg.ContentSecurityPolicy
	if csp == "" {
		csp = "default-src 'none'"
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Strict-Transport-Security", hsts)
			w.Header().Set("X-Frame-Options", frameOptions)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", referrerPolicy)
			w.Header().Set("Content-Security-Policy", csp)
			w.Header().Set("X-XSS-Protection", "0") // modern browsers; rely on CSP
			w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
			next.ServeHTTP(w, r)
		})
	}
}
