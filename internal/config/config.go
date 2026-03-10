package config

import (
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config is the root configuration structure.
type Config struct {
	Server            ServerConfig       `mapstructure:"server"`
	Upstreams         []Upstream         `mapstructure:"upstreams"`
	Routes            []Route            `mapstructure:"routes"`
	Auth              AuthConfig         `mapstructure:"auth"`
	RateLimit         RateLimitConfig    `mapstructure:"rate_limit"`
	RateLimitProfiles []RateLimitProfile `mapstructure:"rate_limit_profiles"`
	Redis             RedisConfig        `mapstructure:"redis"`
	CORS              CORSConfig         `mapstructure:"cors"`
	Security          SecurityConfig     `mapstructure:"security"`
}

type ServerConfig struct {
	Port            int           `mapstructure:"port"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

type Upstream struct {
	Name           string        `mapstructure:"name"`
	URLs           []string      `mapstructure:"urls"`
	CBFailureRatio float64       `mapstructure:"cb_failure_ratio"`
	CBTimeout      time.Duration `mapstructure:"cb_timeout"`
	CBInterval     time.Duration `mapstructure:"cb_interval"`
	CBMinRequests  uint32        `mapstructure:"cb_min_requests"`
}

type Route struct {
	Path             string   `mapstructure:"path"`
	Upstream         string   `mapstructure:"upstream"`
	Auth             bool     `mapstructure:"auth"`
	RateLimitProfile string   `mapstructure:"rate_limit_profile"` // profile id; empty = no rate limiting
	Methods          []string `mapstructure:"methods"`
}

type AuthConfig struct {
	Domain              string        `mapstructure:"domain"`
	Audience            string        `mapstructure:"audience"`
	JWKSRefreshInterval time.Duration `mapstructure:"jwks_refresh_interval"`
}

type RateLimitConfig struct {
	Enabled       bool          `mapstructure:"enabled"`
	DefaultRPS    int           `mapstructure:"default_rps"`
	WindowSize    time.Duration `mapstructure:"window_size"`
	KeyStrategy   string        `mapstructure:"key_strategy"` // ip | user | api_key
	LocalFallback bool          `mapstructure:"local_fallback"`
}

// RateLimitProfile is a named rate limit policy that can be referenced by routes.
type RateLimitProfile struct {
	ID          string        `mapstructure:"id"`
	RPS         int           `mapstructure:"rps"`
	WindowSize  time.Duration `mapstructure:"window_size"`
	KeyStrategy string        `mapstructure:"key_strategy"` // ip | user | api_key; inherits global default if empty
}

type RedisConfig struct {
	Address  string `mapstructure:"address"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
	PoolSize int    `mapstructure:"pool_size"`
}

type CORSConfig struct {
	AllowedOrigins   []string `mapstructure:"allowed_origins"`
	AllowedMethods   []string `mapstructure:"allowed_methods"`
	AllowedHeaders   []string `mapstructure:"allowed_headers"`
	AllowCredentials bool     `mapstructure:"allow_credentials"`
	MaxAge           int      `mapstructure:"max_age"`
}

type SecurityConfig struct {
	IPAllowlist           []string `mapstructure:"ip_allowlist"`
	IPDenylist            []string `mapstructure:"ip_denylist"`
	HSTSMaxAge            int      `mapstructure:"hsts_max_age"`
	FrameOptions          string   `mapstructure:"frame_options"`
	ReferrerPolicy        string   `mapstructure:"referrer_policy"`
	ContentSecurityPolicy string   `mapstructure:"content_security_policy"`
}

// Load reads configuration from path and environment variables.
// Environment variables take precedence and use prefix GATEWAY_ with dots replaced by underscores.
// Example: GATEWAY_SERVER_PORT overrides server.port.
func Load(path string) (*Config, error) {
	v := viper.New()

	// Environment variable configuration
	v.SetEnvPrefix("GATEWAY")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Set defaults
	setDefaults(v)

	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, err
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	if err := Validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.read_timeout", "30s")
	v.SetDefault("server.write_timeout", "30s")
	v.SetDefault("server.shutdown_timeout", "15s")

	v.SetDefault("auth.jwks_refresh_interval", "15m")

	v.SetDefault("rate_limit.enabled", true)
	v.SetDefault("rate_limit.default_rps", 100)
	v.SetDefault("rate_limit.window_size", "1s")
	v.SetDefault("rate_limit.key_strategy", "ip")
	v.SetDefault("rate_limit.local_fallback", true)

	v.SetDefault("redis.address", "redis:6379")
	v.SetDefault("redis.pool_size", 10)

	v.SetDefault("cors.allowed_methods", []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"})
	v.SetDefault("cors.allowed_headers", []string{"Authorization", "Content-Type", "X-Request-ID"})
	v.SetDefault("cors.max_age", 86400)

	v.SetDefault("security.hsts_max_age", 31536000)
	v.SetDefault("security.frame_options", "DENY")
	v.SetDefault("security.referrer_policy", "strict-origin-when-cross-origin")
	v.SetDefault("security.content_security_policy", "default-src 'none'")
}
