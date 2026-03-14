package circuit

import (
	"fmt"
	"sync"
	"time"

	"github.com/lucabartmann/golang-api-gateway/internal/config"
	"github.com/sony/gobreaker"
)

// Manager holds per-upstream circuit breakers and implements BreakerProvider.
type Manager struct {
	mu       sync.RWMutex
	breakers map[string]*gobreaker.CircuitBreaker
}

// NewManager initialises a circuit breaker for each configured upstream.
func NewManager(upstreams []config.Upstream) *Manager {
	m := &Manager{breakers: make(map[string]*gobreaker.CircuitBreaker)}
	for _, u := range upstreams {
		m.breakers[u.Name] = newBreaker(u)
	}
	return m
}

// Execute runs fn through the named upstream's circuit breaker.
func (m *Manager) Execute(upstreamName string, fn func() (interface{}, error)) (interface{}, error) {
	m.mu.RLock()
	cb, ok := m.breakers[upstreamName]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("circuit: unknown upstream %q", upstreamName)
	}
	return cb.Execute(fn)
}

func newBreaker(u config.Upstream) *gobreaker.CircuitBreaker {
	failureRatio := u.CBFailureRatio
	if failureRatio == 0 {
		failureRatio = 0.5
	}
	timeout := u.CBTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	interval := u.CBInterval
	if interval == 0 {
		interval = 60 * time.Second
	}
	minRequests := u.CBMinRequests
	if minRequests == 0 {
		minRequests = 5
	}

	return gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        u.Name,
		MaxRequests: 1, // half-open: allow 1 probe request
		Interval:    interval,
		Timeout:     timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			if counts.Requests < uint32(minRequests) {
				return false
			}
			ratio := float64(counts.TotalFailures) / float64(counts.Requests)
			return ratio >= failureRatio
		},
	})
}
