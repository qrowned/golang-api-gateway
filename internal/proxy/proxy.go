package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"

	"github.com/sony/gobreaker"
	"golang-api-gateway/pkg/logger"
)

type upstreamKey struct{}

// BreakerProvider wraps upstream calls with a circuit breaker.
type BreakerProvider interface {
	Execute(upstreamName string, fn func() (interface{}, error)) (interface{}, error)
}

// WithUpstream stores the upstream name in the request context for the director.
func WithUpstream(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, upstreamKey{}, name)
}

// UpstreamFromContext retrieves the upstream name set by the router.
func UpstreamFromContext(ctx context.Context) string {
	name, _ := ctx.Value(upstreamKey{}).(string)
	return name
}

// ReverseProxy wraps httputil.ReverseProxy with circuit breaking and balancing.
type ReverseProxy struct {
	balancers map[string]Balancer
	breaker   BreakerProvider
	proxy     *httputil.ReverseProxy
	log       *slog.Logger
}

// New constructs a ReverseProxy from per-upstream balancers and a circuit breaker provider.
func New(balancers map[string]Balancer, breaker BreakerProvider, log *slog.Logger) *ReverseProxy {
	rp := &ReverseProxy{
		balancers: balancers,
		breaker:   breaker,
		log:       log,
	}

	rp.proxy = &httputil.ReverseProxy{
		Director:       rp.director,
		ModifyResponse: rp.modifyResponse,
		ErrorHandler:   rp.errorHandler,
	}

	return rp
}

func (rp *ReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	upstreamName := UpstreamFromContext(r.Context())
	if upstreamName == "" {
		writeJSONError(w, http.StatusBadGateway, "no upstream configured for this route")
		return
	}

	_, err := rp.breaker.Execute(upstreamName, func() (interface{}, error) {
		// Use a response recorder to detect 5xx for breaker counting
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		rp.proxy.ServeHTTP(rec, r)
		if rec.code >= 500 {
			return nil, errors.New("upstream returned 5xx")
		}
		return nil, nil
	})

	if err != nil {
		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			writeJSONError(w, http.StatusServiceUnavailable, "upstream circuit open")
			return
		}
		// 5xx already written by inner proxy; nothing more to do
	}
}

func (rp *ReverseProxy) director(req *http.Request) {
	upstreamName := UpstreamFromContext(req.Context())
	bal, ok := rp.balancers[upstreamName]
	if !ok {
		return
	}
	target, err := bal.Next(req)
	if err != nil {
		return
	}

	req.URL.Scheme = target.Scheme
	req.URL.Host = target.Host
	req.Host = target.Host
	if _, ok := req.Header["User-Agent"]; !ok {
		req.Header.Set("User-Agent", "golang-api-gateway/1.0")
	}
}

func (rp *ReverseProxy) modifyResponse(resp *http.Response) error {
	resp.Header.Del("X-Powered-By")
	resp.Header.Del("Server")
	return nil
}

func (rp *ReverseProxy) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	log := logger.FromContext(r.Context())
	log.Error("proxy error", "error", err, "upstream", UpstreamFromContext(r.Context()))
	writeJSONError(w, http.StatusBadGateway, "upstream error")
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// statusRecorder captures the HTTP status code written by the inner handler.
type statusRecorder struct {
	http.ResponseWriter
	code    int
	written bool
}

func (sr *statusRecorder) WriteHeader(code int) {
	if !sr.written {
		sr.code = code
		sr.written = true
	}
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if !sr.written {
		sr.written = true
	}
	return sr.ResponseWriter.Write(b)
}
