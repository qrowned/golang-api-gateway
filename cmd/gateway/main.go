package main

import (
	"context"
	"fmt"
	"os"

	"github.com/lucabartmann/golang-api-gateway/internal/config"
	"github.com/lucabartmann/golang-api-gateway/internal/gateway"
	"github.com/lucabartmann/golang-api-gateway/internal/health"
	authmw "github.com/lucabartmann/golang-api-gateway/internal/middleware/auth"
	"github.com/lucabartmann/golang-api-gateway/internal/middleware/circuit"
	ratelimitmw "github.com/lucabartmann/golang-api-gateway/internal/middleware/ratelimit"
	"github.com/lucabartmann/golang-api-gateway/internal/proxy"
	"github.com/lucabartmann/golang-api-gateway/internal/router"
	"github.com/lucabartmann/golang-api-gateway/pkg/logger"
	"github.com/redis/go-redis/v9"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

// redisPinger adapts redis.Client to health.RedisChecker.
type redisPinger struct{ client *redis.Client }

func (p *redisPinger) Ping(ctx context.Context) error {
	return p.client.Ping(ctx).Err()
}

func run() error {
	log := logger.New()

	// ── Configuration ─────────────────────────────────────────────────────────
	cfgPath := os.Getenv("GATEWAY_CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "config/config.yaml"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log.Info("configuration loaded", "path", cfgPath)

	// ── Redis ─────────────────────────────────────────────────────────────────
	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Address,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		PoolSize: cfg.Redis.PoolSize,
	})

	// ── Rate limiter ──────────────────────────────────────────────────────────
	var limiter ratelimitmw.Limiter
	if cfg.RateLimit.Enabled {
		redisLimiter := ratelimitmw.NewRedisLimiter(redisClient)
		if cfg.RateLimit.LocalFallback {
			limiter = ratelimitmw.NewFallbackLimiter(redisLimiter, ratelimitmw.NewLocalLimiter())
		} else {
			limiter = redisLimiter
		}
	}

	// ── Circuit breaker ───────────────────────────────────────────────────────
	breakerManager := circuit.NewManager(cfg.Upstreams)

	// ── Proxy balancers ───────────────────────────────────────────────────────
	balancers := make(map[string]proxy.Balancer)
	for _, u := range cfg.Upstreams {
		bal, err := proxy.NewRoundRobinBalancer(u.URLs)
		if err != nil {
			return fmt.Errorf("balancer for upstream %q: %w", u.Name, err)
		}
		balancers[u.Name] = bal
	}

	reverseProxy := proxy.New(balancers, breakerManager, log)

	// ── Auth ──────────────────────────────────────────────────────────────────
	var authMiddleware *authmw.Middleware
	if cfg.Auth.Domain != "" {
		authMiddleware, err = authmw.NewMiddleware(cfg.Auth)
		if err != nil {
			return fmt.Errorf("auth middleware: %w", err)
		}
		log.Info("auth middleware initialised", "domain", cfg.Auth.Domain)
	}

	// ── Health ────────────────────────────────────────────────────────────────
	healthHandler := health.NewHandler(&redisPinger{redisClient})

	// ── Router ────────────────────────────────────────────────────────────────
	h, err := router.New(router.Options{
		Config:        cfg,
		Proxy:         reverseProxy,
		Auth:          authMiddleware,
		Limiter:       limiter,
		HealthHandler: healthHandler,
		Log:           log,
	})
	if err != nil {
		return fmt.Errorf("router: %w", err)
	}

	// ── Server ────────────────────────────────────────────────────────────────
	srv := gateway.New(h, cfg.Server.Port, cfg.Server.ReadTimeout, cfg.Server.WriteTimeout, log)
	return srv.Run(cfg.Server.ShutdownTimeout)
}
