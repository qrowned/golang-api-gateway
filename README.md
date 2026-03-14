# Go API Gateway

A production-grade, modular API gateway written in Go. Designed for Kubernetes with distributed rate limiting via Redis, Auth0 JWT authentication, path-based routing, round-robin load balancing, circuit breaking, CORS, IP filtering, and structured security headers.

## Table of Contents

- [Features](#features)
- [Architecture](#architecture)
- [Project Structure](#project-structure)
- [Dependencies](#dependencies)
- [Getting Started](#getting-started)
  - [Prerequisites](#prerequisites)
  - [Local Development](#local-development)
  - [Configuration](#configuration)
  - [Environment Variables](#environment-variables)
- [Middleware Chain](#middleware-chain)
- [Rate Limiting](#rate-limiting)
- [Authentication](#authentication)
- [Upstream Services](#upstream-services)
  - [Client Library](#client-library)
  - [Installation](#installation)
  - [Router wiring](#router-wiring)
  - [HTTP handlers](#http-handlers)
  - [Service layer](#service-layer)
  - [Log correlation](#log-correlation)
  - [Trust boundary](#trust-boundary)
- [Health Probes](#health-probes)
- [Security](#security)
- [Kubernetes Deployment](#kubernetes-deployment)

---

## Features

- **Routing** — Path and method-based routing via [chi](https://github.com/go-chi/chi), wildcard support
- **Load balancing** — Atomic round-robin across multiple upstream URLs (no mutex)
- **Circuit breaker** — Per-upstream breaker via [gobreaker](https://github.com/sony/gobreaker); configurable failure ratio, timeout, and minimum requests
- **Rate limiting** — Redis sliding-window with named profiles per route; automatic fallback to in-process token bucket if Redis is unavailable
- **Authentication** — Auth0 JWT validation via JWKS cache ([jwx/v2](https://github.com/lestrrat-go/jwx)); auto-refresh on unknown key ID
- **CORS** — Preflight and CORS response headers; no external dependencies
- **IP filtering** — CIDR-based allowlist and denylist evaluated before any other processing
- **Security headers** — HSTS, CSP, X-Frame-Options, Referrer-Policy, Permissions-Policy on every response
- **Structured logging** — JSON via `log/slog`; request-scoped logger with `X-Request-ID` correlation
- **Panic recovery** — Stack traces logged server-side, never leaked to clients
- **Health probes** — `/healthz` (liveness) and `/readyz` (readiness with Redis ping)
- **Graceful shutdown** — SIGTERM drains in-flight requests within a configurable timeout
- **Kubernetes-native** — Distroless image, non-root user, read-only filesystem, liveness/readiness probes, ConfigMap-mounted config

---

## Architecture

```
                        ┌─────────────────────────────────────────────┐
                        │               API Gateway                    │
                        │                                              │
Internet ──────────────▶│  [1] Recovery                               │
                        │  [2] Request ID + Logging                   │
                        │  [3] Security Headers                        │
                        │  [4] IP Filter          (CIDR block/allow)  │
                        │  [5] CORS               (preflight)         │
                        │  [6] Rate Limiter ──────────────────────────│──▶ Redis
                        │  [7] Auth (JWT/Auth0) ──────────────────────│──▶ JWKS
                        │  [8] Router                                  │
                        │  [9] Circuit Breaker                         │
                        └──────────────┬──────────────────────────────┘
                                       │
                     ┌─────────────────┼──────────────┐
                     ▼                 ▼               ▼
               users-service    orders-service    other-service
```

**Rate limiting runs before authentication** to protect the cryptographically expensive JWT verification operation from credential-stuffing attacks.

---

## Project Structure

```
github.com/lucabartmann/golang-api-gateway/
├── cmd/
│   └── gateway/
│       └── main.go                  # entry point, dependency wiring
├── internal/
│   ├── config/
│   │   ├── config.go                # Config structs + Load() via Viper
│   │   └── validate.go              # fail-fast validation at startup
│   ├── gateway/
│   │   └── server.go                # http.Server lifecycle, SIGTERM graceful shutdown
│   ├── proxy/
│   │   ├── proxy.go                 # ReverseProxy with circuit breaker integration
│   │   └── balancer.go              # RoundRobinBalancer (atomic, no mutex)
│   ├── router/
│   │   └── router.go                # chi router, middleware wiring
│   ├── middleware/
│   │   ├── auth/
│   │   │   └── auth0.go             # JWKS cache + JWT RS256/ES256 validation
│   │   ├── ratelimit/
│   │   │   ├── limiter.go           # RedisLimiter (Lua) + LocalLimiter + FallbackLimiter
│   │   │   └── middleware.go        # 429 enforcement, rate limit response headers
│   │   ├── cors/
│   │   │   └── middleware.go        # preflight + CORS headers (no external deps)
│   │   ├── security/
│   │   │   ├── headers.go           # HSTS, CSP, X-Frame-Options, etc.
│   │   │   └── ipfilter.go          # CIDR allowlist/denylist
│   │   ├── circuit/
│   │   │   └── breaker.go           # per-upstream gobreaker manager
│   │   ├── logging/
│   │   │   └── middleware.go        # structured logging, X-Request-ID injection
│   │   └── recovery/
│   │       └── middleware.go        # panic → 500, stack trace logged not leaked
│   └── health/
│       └── handler.go               # /healthz + /readyz
├── pkg/
│   ├── gateway/
│   │   ├── identity.go              # Identity struct, FromContext, MustFromContext, sentinel errors
│   │   ├── middleware.go            # Middleware (header extraction), Require, RequireAuthenticated
│   │   └── helpers.go              # UserID(ctx), RequestID(ctx), CheckScope(ctx, scope)
│   └── logger/
│       └── logger.go                # slog JSON logger, FromContext/WithContext
├── config/
│   └── config.example.yaml          # fully documented reference config
├── deploy/
│   └── kubernetes/
│       ├── deployment.yaml
│       ├── service.yaml
│       └── configmap.yaml
└── Dockerfile                       # multi-stage, distroless runtime
```

---

## Dependencies

| Package | Version | Purpose |
|---|---|---|
| `github.com/go-chi/chi/v5` | v5.2.1 | HTTP router and middleware composition |
| `github.com/redis/go-redis/v9` | v9.8.0 | Redis client for distributed rate limiting |
| `github.com/sony/gobreaker` | v1.0.0 | Circuit breaker per upstream |
| `github.com/spf13/viper` | v1.20.1 | YAML config loading with env var overrides |
| `github.com/lestrrat-go/jwx/v2` | v2.1.6 | JWKS cache and JWT RS256/ES256 validation |
| `golang.org/x/time/rate` | v0.11.0 | Token bucket for local rate limit fallback |

---

## Getting Started

### Prerequisites

- Go 1.25+
- Docker and Docker Compose
- A Redis instance (provided via Docker Compose below)
- An Auth0 tenant (optional; disable `auth: true` on routes to run without it)

### Local Development

**1. Copy and edit the config:**

```bash
cp config/config.example.yaml config/local.yaml
# edit config/local.yaml with your upstreams, auth domain, etc.
```

**2. Start dependencies and the gateway:**

Create `docker-compose.yml`:

```yaml
services:
  redis:
    image: redis:7-alpine
    ports: ["6379:6379"]

  echo:
    image: ealen/echo-server
    ports: ["8081:80"]

  gateway:
    build: .
    ports: ["8080:8080"]
    environment:
      GATEWAY_CONFIG_PATH: /config/config.yaml
    volumes:
      - ./config/local.yaml:/config/config.yaml:ro
    depends_on: [redis, echo]
```

```bash
docker compose up --build
```

**3. Verify:**

```bash
curl http://localhost:8080/healthz
# {"status":"ok"}

curl http://localhost:8080/readyz
# {"status":"ok"}  (or degraded if Redis unreachable)

curl http://localhost:8080/api/v1/test/hello
# proxied to echo upstream
```

**4. Build locally:**

```bash
go build ./cmd/gateway
go vet ./...
```

---

## Configuration

The gateway reads a single YAML file. The path defaults to `config/config.yaml` and can be overridden with `GATEWAY_CONFIG_PATH`.

```yaml
server:
  port: 8080
  read_timeout: 30s
  write_timeout: 30s
  shutdown_timeout: 15s       # must be < Kubernetes terminationGracePeriodSeconds

upstreams:
  - name: users-service
    urls:
      - http://users-svc-1:8080
      - http://users-svc-2:8080
    cb_failure_ratio: 0.5     # open circuit when ≥50% of requests fail
    cb_timeout: 30s           # time circuit stays open before half-open probe
    cb_interval: 60s          # rolling window for failure counting
    cb_min_requests: 5        # minimum requests before ratio is evaluated

routes:
  - path: /api/v1/users/*
    upstream: users-service
    auth: true                # require valid JWT
    rate_limit_profile: default
    methods: [GET, POST, PUT, DELETE]

auth:
  domain: "your-tenant.auth0.com"   # no https://
  audience: "https://api.your-domain.com"
  jwks_refresh_interval: 15m

rate_limit:
  enabled: true
  default_rps: 100            # used by the implicit "default" profile
  window_size: 1s
  key_strategy: ip            # ip | user | api_key
  local_fallback: true

rate_limit_profiles:
  - id: strict
    rps: 10
    window_size: 1s
    key_strategy: ip
  - id: per-user
    rps: 50
    window_size: 1s
    key_strategy: user        # bucketed by X-User-ID header

redis:
  address: "redis:6379"
  password: ""
  db: 0
  pool_size: 10

cors:
  allowed_origins: ["https://app.your-domain.com"]
  allowed_methods: [GET, POST, PUT, DELETE, OPTIONS]
  allowed_headers: [Authorization, Content-Type, X-Request-ID]
  allow_credentials: true
  max_age: 86400

security:
  ip_denylist: ["203.0.113.0/24"]
  ip_allowlist: []            # empty = allow all (after denylist)
  hsts_max_age: 31536000
  frame_options: "DENY"
  referrer_policy: "strict-origin-when-cross-origin"
  content_security_policy: "default-src 'none'"
```

See [`config/config.example.yaml`](config/config.example.yaml) for the fully annotated reference.

### Environment Variables

All config keys can be overridden via environment variables using the prefix `GATEWAY_` with dots replaced by underscores:

| Environment Variable | Config key | Example |
|---|---|---|
| `GATEWAY_CONFIG_PATH` | — | `/config/config.yaml` |
| `GATEWAY_SERVER_PORT` | `server.port` | `8080` |
| `GATEWAY_REDIS_ADDRESS` | `redis.address` | `redis:6379` |
| `GATEWAY_REDIS_PASSWORD` | `redis.password` | `secret` |
| `GATEWAY_AUTH_DOMAIN` | `auth.domain` | `tenant.auth0.com` |
| `GATEWAY_AUTH_AUDIENCE` | `auth.audience` | `https://api.example.com` |

Environment variables always take precedence over the config file.

---

## Middleware Chain

The order is security-critical and fixed:

| # | Middleware | Scope | Rationale |
|---|---|---|---|
| 1 | **Recovery** | Global | Outermost — catches panics in all inner layers |
| 2 | **Request ID + Logging** | Global | Logs every request including rejected ones |
| 3 | **Security Headers** | Global | Applied to all responses before anything can short-circuit |
| 4 | **IP Filter** | Global | Block before any expensive processing |
| 5 | **CORS** | Global | Preflight must be answered before rate limiting |
| 6 | **Rate Limiter** | Per-route | Before auth — protects JWT verification from brute force |
| 7 | **Auth (JWT)** | Per-route | Only reached by rate-allowed requests |
| 8 | **Proxy → Circuit Breaker** | Per-request | Wraps the actual upstream HTTP call |

---

## Rate Limiting

Rate limiting uses a **Redis sliding-window** implemented as an atomic Lua script (`ZREMRANGEBYSCORE` + `ZCARD` + `ZADD` in one round trip). On Redis failure, it automatically falls back to a per-process token bucket.

### Named profiles

Define profiles in config and reference them by ID on each route:

```yaml
rate_limit_profiles:
  - id: strict       # 10 req/s per IP
    rps: 10
    window_size: 1s
    key_strategy: ip

  - id: per-user     # 50 req/s per authenticated user
    rps: 50
    window_size: 1s
    key_strategy: user

routes:
  - path: /api/v1/admin/*
    rate_limit_profile: strict
  - path: /api/v1/users/*
    rate_limit_profile: per-user
```

The `"default"` profile is always available and uses the global `rate_limit` block values.

### Key strategies

| Strategy | Key basis | Notes |
|---|---|---|
| `ip` | Client IP (respects `X-Forwarded-For`) | Default |
| `user` | `X-User-ID` header (set by auth middleware) | Falls back to IP if header absent |
| `api_key` | `X-API-Key` header | Falls back to IP if header absent |

### Response headers

```
X-RateLimit-Limit: 10
X-RateLimit-Remaining: 7
X-RateLimit-Reset: 1741609200
X-RateLimit-Profile: strict
Retry-After: 3              # only present on 429; actual time until oldest entry expires
```

---

## Authentication

Auth0 JWT validation using RS256/ES256 (JWKS). Configure your Auth0 tenant:

1. Create an **API** in Auth0 dashboard (Applications → APIs)
2. Set the identifier as your `audience`
3. Ensure your application uses **RS256** signing (Advanced Settings → OAuth)

```yaml
auth:
  domain: "your-tenant.auth0.com"
  audience: "https://api.your-domain.com"
  jwks_refresh_interval: 15m
```

On successful validation the gateway forwards to upstreams:

| Header | Value |
|---|---|
| `X-User-ID` | JWT `sub` claim |
| `X-User-Scopes` | JWT `scope` claim (space-separated) |
| `X-Request-Id` | Unique request ID for log correlation |

**Getting a token for testing (Resource Owner Password Grant):**

> Requires the Password grant enabled on your Auth0 application and a Default Directory configured in Tenant Settings.

```bash
curl -X POST "https://your-tenant.auth0.com/oauth/token" \
  -H "Content-Type: application/json" \
  -d '{
    "grant_type": "password",
    "username": "user@example.com",
    "password": "yourpassword",
    "audience": "https://api.your-domain.com",
    "client_id": "YOUR_CLIENT_ID",
    "client_secret": "YOUR_CLIENT_SECRET",
    "scope": "openid profile email"
  }'
```

---

## Upstream Services

After the gateway validates a request it forwards the caller's identity to upstreams via HTTP headers. Upstream services should never re-validate the JWT — instead they consume these headers through the provided client library.

| Header forwarded by gateway | Content |
|---|---|
| `X-User-ID` | JWT `sub` claim (e.g. `auth0\|64a1b2c3`) |
| `X-User-Scopes` | Space-separated OAuth2 scopes (e.g. `read:orders write:orders`) |
| `X-Request-Id` | Unique request ID for cross-service log correlation |

### Client Library

The `pkg/gateway` package is a zero-dependency client library that upstream services import to consume these headers through a typed API. It handles header extraction, context propagation, scope enforcement, and service-layer authorization checks.

```
pkg/gateway/
├── identity.go    # Identity struct, FromContext, MustFromContext, sentinel errors
├── middleware.go  # HTTP middleware: Middleware, Require, RequireAuthenticated
└── helpers.go     # One-liner helpers: UserID, RequestID, CheckScope
```

### Installation

**Same repository:**

```go
import "github.com/lucabartmann/golang-api-gateway/pkg/gateway"
```

**Separate repository:**

```bash
go get github.com/lucabartmann/golang-api-gateway/pkg/gateway@latest
```

The package has no external dependencies beyond the Go standard library.

### Router wiring

Add `gateway.Middleware` as the first middleware in your router. It extracts the three gateway headers into the request context once so all downstream handlers and service-layer code can read them without touching `http.Request`.

```go
r := chi.NewRouter()

// Always first — populates context for all handlers below
r.Use(gateway.Middleware)

// No scope check — any authenticated or unauthenticated request passes
r.Get("/healthz", healthHandler)

// Requires a logged-in user (non-empty X-User-ID), no specific scope
r.With(gateway.RequireAuthenticated()).Get("/profile", getProfile)

// Requires one specific scope
r.With(gateway.Require("read:orders")).Get("/orders", listOrders)

// Requires multiple scopes — all must be present
r.With(gateway.Require("read:orders", "write:orders")).Put("/orders/{id}", updateOrder)
```

`Require` responds automatically:
- `401 {"error":"unauthenticated"}` — `X-User-ID` is absent (public route, or gateway auth disabled)
- `403 {"error":"missing scope: write:orders"}` — user is authenticated but lacks the scope

### HTTP handlers

Two ways to read the identity inside a handler:

```go
// Safe form — use when the route may be unauthenticated
func listOrders(w http.ResponseWriter, r *http.Request) {
    id, ok := gateway.FromContext(r.Context())
    if !ok || !id.IsAuthenticated() {
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        return
    }
    log.Printf("[%s] listing orders for user %s", id.RequestID, id.UserID)
    // ...
}

// Panic form — use only in handlers already guarded by Require or RequireAuthenticated
func getProfile(w http.ResponseWriter, r *http.Request) {
    id := gateway.MustFromContext(r.Context()) // panics if Middleware not installed
    fmt.Fprintf(w, "hello %s, you have scopes: %v", id.UserID, id.Scopes)
}

// Manual scope check inside a handler (alternative to Require middleware)
func deleteUser(w http.ResponseWriter, r *http.Request) {
    id := gateway.MustFromContext(r.Context())
    if !id.HasScope("delete:users") {
        http.Error(w, "forbidden", http.StatusForbidden)
        return
    }
    // ...
}
```

### Service layer

Service-layer code that cannot depend on `http.Handler` uses `CheckScope` and the one-liner helpers. The context is passed through from the handler, so no HTTP types are needed:

```go
func (s *OrderService) Cancel(ctx context.Context, orderID string) error {
    // Returns gateway.ErrUnauthenticated or gateway.ErrForbidden
    if err := gateway.CheckScope(ctx, "write:orders"); err != nil {
        return err
    }
    return s.db.CancelOrder(ctx, orderID, gateway.UserID(ctx))
}

func (s *OrderService) Get(ctx context.Context, orderID string) (*Order, error) {
    order, err := s.db.GetOrder(ctx, orderID)
    if err != nil {
        return nil, err
    }
    // Restrict to owner unless the caller has admin scope
    if order.OwnerID != gateway.UserID(ctx) {
        if err := gateway.CheckScope(ctx, "admin:orders"); err != nil {
            return nil, gateway.ErrForbidden
        }
    }
    return order, nil
}
```

Mapping the sentinel errors to HTTP responses at the handler boundary:

```go
order, err := orderService.Get(r.Context(), orderID)
if errors.Is(err, gateway.ErrUnauthenticated) {
    http.Error(w, "unauthorized", http.StatusUnauthorized)
    return
}
if errors.Is(err, gateway.ErrForbidden) {
    http.Error(w, "forbidden", http.StatusForbidden)
    return
}
```

### Log correlation

Propagate `X-Request-Id` into every log line so requests can be traced across the gateway and all upstream services in any log aggregator:

```go
func loggingMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            reqLog := log.With(
                "request_id", gateway.RequestID(r.Context()),
                "user_id",    gateway.UserID(r.Context()),
            )
            // store reqLog in context for use in service layer
            next.ServeHTTP(w, r)
        })
    }
}
```

### Trust boundary

> **Important:** `X-User-ID` and `X-User-Scopes` are plain HTTP headers. Any caller with direct network access to an upstream service can forge them. Upstream services must be reachable **only through the gateway**.

Enforce this with a Kubernetes `NetworkPolicy`:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: orders-service-ingress
spec:
  podSelector:
    matchLabels:
      app: orders-service
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app: api-gateway
```

---

## Health Probes

| Endpoint | Type | Returns |
|---|---|---|
| `GET /healthz` | Liveness | `200 {"status":"ok"}` always |
| `GET /readyz` | Readiness | `200` if Redis reachable, `503 {"status":"degraded","reason":"..."}` otherwise |

These endpoints bypass all rate limiting and authentication.

---

## Security

### Response headers (all responses)

| Header | Value |
|---|---|
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` |
| `X-Frame-Options` | `DENY` |
| `X-Content-Type-Options` | `nosniff` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` |
| `Content-Security-Policy` | `default-src 'none'` |
| `Permissions-Policy` | `geolocation=(), microphone=(), camera=()` |

### Upstream response headers stripped

The gateway removes `Server` and `X-Powered-By` from all upstream responses before forwarding to clients.

### Circuit breaker

Each upstream has an independent circuit breaker. When the failure ratio exceeds the threshold, the circuit opens and requests immediately return `503` without hitting the upstream. After the timeout the circuit moves to half-open and probes with one request before fully recovering.

```
Closed ──(failure ratio ≥ threshold)──▶ Open ──(timeout)──▶ Half-Open
  ▲                                                               │
  └──────────────────(probe succeeds)────────────────────────────┘
```

---

## Kubernetes Deployment

```bash
# Apply all manifests
kubectl apply -f deploy/kubernetes/

# Check rollout
kubectl rollout status deployment/api-gateway

# View logs
kubectl logs -l app=api-gateway -f
```

### Secrets

Sensitive config (Redis credentials, Auth0 domain/audience) can be injected as environment variables from a Kubernetes Secret rather than baked into the ConfigMap:

```bash
kubectl create secret generic gateway-secrets \
  --from-literal=redis-address=redis:6379 \
  --from-literal=auth-domain=your-tenant.auth0.com \
  --from-literal=auth-audience=https://api.your-domain.com
```

### Resource limits

The deployment requests `100m` CPU / `64Mi` memory and limits to `500m` CPU / `256Mi` memory. The gateway itself is stateless; scale horizontally by increasing `replicas`. Redis holds all shared rate limit state so counters remain accurate across replicas.

### Shutdown behaviour

`terminationGracePeriodSeconds: 30` > `server.shutdown_timeout: 15s`. When Kubernetes sends SIGTERM the gateway stops accepting new connections and waits up to 15 seconds for in-flight requests to complete before exiting cleanly.

### Building and pushing the image

```bash
docker build -t your-registry/api-gateway:latest .
docker push your-registry/api-gateway:latest

# Update the image in the deployment
kubectl set image deployment/api-gateway gateway=your-registry/api-gateway:latest
```
