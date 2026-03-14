package security

import (
	"encoding/json"
	"net"
	"net/http"

	"github.com/lucabartmann/golang-api-gateway/internal/config"
	"github.com/lucabartmann/golang-api-gateway/pkg/logger"
)

// IPFilterMiddleware blocks or allows requests based on CIDR allowlist/denylist.
// Denylist is checked first. If an allowlist is configured, only listed CIDRs pass.
func IPFilterMiddleware(cfg config.SecurityConfig) (func(http.Handler) http.Handler, error) {
	denylist, err := parseCIDRs(cfg.IPDenylist)
	if err != nil {
		return nil, err
	}
	allowlist, err := parseCIDRs(cfg.IPAllowlist)
	if err != nil {
		return nil, err
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip, err := clientIP(r)
			if err != nil {
				log := logger.FromContext(r.Context())
				log.Warn("ip filter: could not parse client IP", "error", err)
				writeIPError(w)
				return
			}

			// Denylist takes priority
			for _, cidr := range denylist {
				if cidr.Contains(ip) {
					writeIPError(w)
					return
				}
			}

			// Allowlist: if non-empty, IP must be in it
			if len(allowlist) > 0 {
				allowed := false
				for _, cidr := range allowlist {
					if cidr.Contains(ip) {
						allowed = true
						break
					}
				}
				if !allowed {
					writeIPError(w)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}, nil
}

func clientIP(r *http.Request) (net.IP, error) {
	// Trust X-Forwarded-For only if set (behind a trusted load balancer)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip := net.ParseIP(xff)
		if ip != nil {
			return ip, nil
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return net.ParseIP(r.RemoteAddr), nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, &net.AddrError{Err: "invalid IP", Addr: host}
	}
	return ip, nil
}

func parseCIDRs(cidrs []string) ([]*net.IPNet, error) {
	result := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, network, err := net.ParseCIDR(c)
		if err != nil {
			return nil, err
		}
		result = append(result, network)
	}
	return result, nil
}

func writeIPError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
}
