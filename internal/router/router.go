package router

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"golang-api-gateway/internal/config"
	"golang-api-gateway/internal/health"
	authmw "golang-api-gateway/internal/middleware/auth"
	"golang-api-gateway/internal/middleware/cors"
	"golang-api-gateway/internal/middleware/logging"
	ratelimitmw "golang-api-gateway/internal/middleware/ratelimit"
	"golang-api-gateway/internal/middleware/recovery"
	"golang-api-gateway/internal/middleware/security"
	"golang-api-gateway/internal/proxy"
)

// Options bundles the dependencies needed to build the router.
type Options struct {
	Config        *config.Config
	Proxy         *proxy.ReverseProxy
	Auth          *authmw.Middleware  // nil if auth not configured
	Limiter       ratelimitmw.Limiter // nil if rate limiting disabled
	HealthHandler *health.Handler
	Log           *slog.Logger
}

// New constructs the chi router with the full middleware chain.
func New(opts Options) (http.Handler, error) {
	cfg := opts.Config

	ipFilter, err := security.IPFilterMiddleware(cfg.Security)
	if err != nil {
		return nil, err
	}

	// Build rate limit profile lookup map.
	// "default" is synthesised from the global rate_limit block for backward compat.
	profiles := buildProfileMap(cfg)

	r := chi.NewRouter()

	// ── Global middleware chain (order is security-critical) ──────────────────
	r.Use(recovery.Middleware)                      // [1] outermost — catches panics in all layers
	r.Use(chimw.RequestID)                          // [2] inject X-Request-ID
	r.Use(logging.Middleware(opts.Log))             // [3] structured logging
	r.Use(security.HeadersMiddleware(cfg.Security)) // [4] security headers on ALL responses
	r.Use(ipFilter)                                 // [5] block by IP before any expensive work
	r.Use(cors.Middleware(cfg.CORS))                // [6] CORS preflight (not rate-limited)

	// ── Health probes (unauthenticated, not rate-limited) ─────────────────────
	r.Get("/healthz", opts.HealthHandler.Liveness)
	r.Get("/readyz", opts.HealthHandler.Readiness)

	// ── Proxied routes ────────────────────────────────────────────────────────
	for _, route := range cfg.Routes {
		route := route

		methods := route.Methods
		if len(methods) == 0 {
			methods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete}
		}

		var routeMiddlewares []func(http.Handler) http.Handler

		// [7] Rate limiting — before auth, protects JWT verify from brute force
		if pid := route.RateLimitProfile; pid != "" && opts.Limiter != nil && cfg.RateLimit.Enabled {
			if profile, ok := profiles[pid]; ok {
				routeMiddlewares = append(routeMiddlewares, ratelimitmw.Middleware(opts.Limiter, profile))
			}
		}

		// [8] JWT authentication
		if route.Auth && opts.Auth != nil {
			routeMiddlewares = append(routeMiddlewares, opts.Auth.Handler)
		}

		upstreamName := route.Upstream
		handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := proxy.WithUpstream(req.Context(), upstreamName)
			opts.Proxy.ServeHTTP(w, req.WithContext(ctx))
		})

		for _, method := range methods {
			method := method
			r.Method(method, route.Path, chain(handler, routeMiddlewares...))
		}
	}

	return r, nil
}

// buildProfileMap returns a map of profile ID → RateLimitProfile.
// It always includes "default", synthesised from the global rate_limit block.
func buildProfileMap(cfg *config.Config) map[string]config.RateLimitProfile {
	globalStrategy := cfg.RateLimit.KeyStrategy
	if globalStrategy == "" {
		globalStrategy = "ip"
	}

	profiles := map[string]config.RateLimitProfile{
		"default": {
			ID:          "default",
			RPS:         cfg.RateLimit.DefaultRPS,
			WindowSize:  cfg.RateLimit.WindowSize,
			KeyStrategy: globalStrategy,
		},
	}

	for _, p := range cfg.RateLimitProfiles {
		// Inherit global key_strategy if not set on the profile
		if p.KeyStrategy == "" {
			p.KeyStrategy = globalStrategy
		}
		profiles[p.ID] = p
	}

	return profiles
}

// chain applies middlewares in order (first middleware is outermost).
func chain(h http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}
