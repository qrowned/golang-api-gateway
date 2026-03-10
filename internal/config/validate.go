package config

import (
	"errors"
	"fmt"
	"net/url"
)

// Validate performs fail-fast validation of the loaded configuration.
func Validate(cfg *Config) error {
	var errs []error

	if cfg.Server.Port < 1 || cfg.Server.Port > 65535 {
		errs = append(errs, fmt.Errorf("server.port must be between 1 and 65535, got %d", cfg.Server.Port))
	}

	if cfg.Server.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("server.shutdown_timeout must be positive"))
	}

	upstreamNames := make(map[string]struct{})
	for i, u := range cfg.Upstreams {
		if u.Name == "" {
			errs = append(errs, fmt.Errorf("upstreams[%d].name is required", i))
			continue
		}
		if _, dup := upstreamNames[u.Name]; dup {
			errs = append(errs, fmt.Errorf("duplicate upstream name: %q", u.Name))
		}
		upstreamNames[u.Name] = struct{}{}

		if len(u.URLs) == 0 {
			errs = append(errs, fmt.Errorf("upstream %q: at least one URL required", u.Name))
		}
		for j, rawURL := range u.URLs {
			if _, err := url.ParseRequestURI(rawURL); err != nil {
				errs = append(errs, fmt.Errorf("upstream %q url[%d] %q is invalid: %w", u.Name, j, rawURL, err))
			}
		}
		if u.CBFailureRatio < 0 || u.CBFailureRatio > 1 {
			errs = append(errs, fmt.Errorf("upstream %q: cb_failure_ratio must be between 0 and 1", u.Name))
		}
	}

	// Validate and index rate limit profiles (including the implicit "default")
	profileIDs := make(map[string]struct{})
	profileIDs["default"] = struct{}{} // synthesised from rate_limit global block
	for i, p := range cfg.RateLimitProfiles {
		if p.ID == "" {
			errs = append(errs, fmt.Errorf("rate_limit_profiles[%d].id is required", i))
			continue
		}
		if p.ID == "default" {
			errs = append(errs, errors.New(`rate_limit_profiles: "default" is reserved; configure it via the rate_limit block`))
			continue
		}
		if _, dup := profileIDs[p.ID]; dup {
			errs = append(errs, fmt.Errorf("duplicate rate_limit_profiles id: %q", p.ID))
		}
		profileIDs[p.ID] = struct{}{}
		if p.RPS <= 0 {
			errs = append(errs, fmt.Errorf("rate_limit_profiles[%d] %q: rps must be positive", i, p.ID))
		}
		if s := p.KeyStrategy; s != "" && s != "ip" && s != "user" && s != "api_key" {
			errs = append(errs, fmt.Errorf("rate_limit_profiles[%d] %q: key_strategy must be ip, user, or api_key; got %q", i, p.ID, s))
		}
	}

	for i, r := range cfg.Routes {
		if r.Path == "" {
			errs = append(errs, fmt.Errorf("routes[%d].path is required", i))
		}
		if r.Upstream == "" {
			errs = append(errs, fmt.Errorf("routes[%d].upstream is required", i))
		} else if _, ok := upstreamNames[r.Upstream]; !ok {
			errs = append(errs, fmt.Errorf("route %q references unknown upstream %q", r.Path, r.Upstream))
		}
		if pid := r.RateLimitProfile; pid != "" {
			if _, ok := profileIDs[pid]; !ok {
				errs = append(errs, fmt.Errorf("route %q references unknown rate_limit_profile %q", r.Path, pid))
			}
		}
	}

	if cfg.Auth.Domain != "" && cfg.Auth.Audience == "" {
		errs = append(errs, errors.New("auth.audience is required when auth.domain is set"))
	}

	strategy := cfg.RateLimit.KeyStrategy
	if strategy != "" && strategy != "ip" && strategy != "user" && strategy != "api_key" {
		errs = append(errs, fmt.Errorf("rate_limit.key_strategy must be ip, user, or api_key; got %q", strategy))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
