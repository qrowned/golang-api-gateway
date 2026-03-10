package proxy

import (
	"errors"
	"net/http"
	"net/url"
	"sync/atomic"
)

// Balancer selects the next upstream URL for a given request.
type Balancer interface {
	Next(r *http.Request) (*url.URL, error)
}

// RoundRobinBalancer cycles through upstream URLs atomically without a mutex.
type RoundRobinBalancer struct {
	urls    []*url.URL
	counter atomic.Uint64
}

// NewRoundRobinBalancer parses rawURLs and returns a balancer.
func NewRoundRobinBalancer(rawURLs []string) (*RoundRobinBalancer, error) {
	if len(rawURLs) == 0 {
		return nil, errors.New("balancer: at least one URL required")
	}
	urls := make([]*url.URL, 0, len(rawURLs))
	for _, raw := range rawURLs {
		u, err := url.ParseRequestURI(raw)
		if err != nil {
			return nil, err
		}
		urls = append(urls, u)
	}
	return &RoundRobinBalancer{urls: urls}, nil
}

// Next returns the next URL in round-robin order.
func (b *RoundRobinBalancer) Next(_ *http.Request) (*url.URL, error) {
	idx := b.counter.Add(1) - 1
	return b.urls[idx%uint64(len(b.urls))], nil
}
