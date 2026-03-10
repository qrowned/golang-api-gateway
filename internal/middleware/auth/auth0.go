package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang-api-gateway/internal/config"
	"golang-api-gateway/pkg/logger"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

type claimsKey struct{}

// Claims holds validated JWT claims forwarded to upstreams.
type Claims struct {
	Subject string
	Scopes  []string
}

// FromContext retrieves JWT claims set by the auth middleware.
func FromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsKey{}).(*Claims)
	return c, ok
}

// Middleware validates Auth0 JWTs using a cached JWKS.
// Stores validated claims in context and forwards X-User-ID + X-User-Scopes to upstream.
type Middleware struct {
	cache    *jwk.Cache
	jwksURL  string
	audience string
	issuer   string
}

// NewMiddleware initialises the JWKS cache and starts background refresh.
func NewMiddleware(cfg config.AuthConfig) (*Middleware, error) {
	if cfg.Domain == "" {
		return nil, errors.New("auth: domain is required")
	}
	jwksURL := fmt.Sprintf("https://%s/.well-known/jwks.json", cfg.Domain)
	issuer := fmt.Sprintf("https://%s/", cfg.Domain)

	refreshInterval := cfg.JWKSRefreshInterval
	if refreshInterval == 0 {
		refreshInterval = 15 * time.Minute
	}

	cache := jwk.NewCache(context.Background())

	if err := cache.Register(jwksURL, jwk.WithRefreshInterval(refreshInterval)); err != nil {
		return nil, fmt.Errorf("auth: failed to register JWKS URL: %w", err)
	}

	// Pre-warm the cache
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := cache.Refresh(ctx, jwksURL); err != nil {
		// Non-fatal: cache will retry; log warning at startup
		fmt.Printf("auth: JWKS pre-warm failed (will retry): %v\n", err)
	}

	return &Middleware{
		cache:    cache,
		jwksURL:  jwksURL,
		audience: cfg.Audience,
		issuer:   issuer,
	}, nil
}

// Handler returns an HTTP middleware that enforces JWT authentication.
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log := logger.FromContext(r.Context())

		raw, err := extractBearer(r)
		if err != nil {
			log.Warn("auth: missing or malformed Authorization header", "error", err)
			writeAuthError(w, "missing or invalid Authorization header")
			return
		}

		keySet, err := m.cache.Get(r.Context(), m.jwksURL)
		if err != nil {
			log.Error("auth: failed to fetch JWKS", "error", err)
			writeAuthError(w, "authentication service unavailable")
			return
		}

		token, err := jwt.Parse([]byte(raw),
			jwt.WithKeySet(keySet),
			jwt.WithAudience(m.audience),
			jwt.WithIssuer(m.issuer),
			jwt.WithValidate(true),
		)
		if err != nil {
			// Unknown kid: attempt one forced refresh then retry
			if strings.Contains(err.Error(), "failed to find key") {
				ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				refreshed, refreshErr := m.cache.Refresh(ctx, m.jwksURL)
				cancel()
				if refreshErr == nil {
					token, err = jwt.Parse([]byte(raw),
						jwt.WithKeySet(refreshed),
						jwt.WithAudience(m.audience),
						jwt.WithIssuer(m.issuer),
						jwt.WithValidate(true),
					)
				}
			}
			if err != nil {
				log.Warn("auth: JWT validation failed", "error", err)
				writeAuthError(w, "invalid token")
				return
			}
		}

		claims := &Claims{
			Subject: token.Subject(),
			Scopes:  extractScopes(token),
		}

		ctx := context.WithValue(r.Context(), claimsKey{}, claims)

		// Forward identity headers to upstreams
		r = r.WithContext(ctx)
		log.Info("User id is " + claims.Subject)
		r.Header.Set("X-User-ID", claims.Subject)
		r.Header.Set("X-User-Scopes", strings.Join(claims.Scopes, " "))

		next.ServeHTTP(w, r)
	})
}

func extractBearer(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", errors.New("Authorization header missing")
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", errors.New("Authorization header must be 'Bearer <token>'")
	}
	return parts[1], nil
}

func extractScopes(token jwt.Token) []string {
	raw, ok := token.Get("scope")
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil
		}
		return strings.Fields(v)
	case []interface{}:
		scopes := make([]string, 0, len(v))
		for _, s := range v {
			if str, ok := s.(string); ok {
				scopes = append(scopes, str)
			}
		}
		return scopes
	}
	return nil
}

func writeAuthError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
