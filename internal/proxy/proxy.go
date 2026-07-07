// Package proxy implements Fulcrum's HTTP reverse proxy: it selects a backend
// via the configured balancing strategy, forwards the request, and on an
// upstream failure retries on another healthy backend within a bounded budget.
package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/dylanpatriarchi/fulcrum/internal/balancer"
	"github.com/dylanpatriarchi/fulcrum/internal/metrics"
)

// maxRetryBodyBytes caps how much of a request body is buffered for replay on
// retry. Requests whose body exceeds this are forwarded once, without retry.
const maxRetryBodyBytes = 1 << 20 // 1 MiB

// errRetryableStatus is returned from ModifyResponse to turn a gateway-class
// upstream status (502/503/504) into a retryable failure without writing it to
// the client, so failover can try another backend.
var errRetryableStatus = errors.New("proxy: retryable upstream status")

// Options tunes the proxy's forwarding, retry and shedding behaviour.
type Options struct {
	MaxRetries            int
	RetryNonIdempotent    bool
	MaxInFlightPerBackend int
	PassiveMaxFails       int
	RequestTimeout        time.Duration
}

// Proxy forwards each request to a strategy-chosen healthy backend, with bounded
// retry/failover and passive health marking.
type Proxy struct {
	pool     *balancer.Pool
	strategy balancer.Strategy
	opts     Options
	log      *slog.Logger
	metrics  *metrics.Metrics

	// proxies holds one ReverseProxy per backend, built once at construction.
	// Read-only after New returns, so it needs no synchronisation.
	proxies map[*balancer.Backend]*httputil.ReverseProxy
}

// attemptState carries a single attempt's upstream error from the ReverseProxy
// ErrorHandler back to the orchestration loop, via the request context.
type attemptState struct{ err error }
type attemptCtxKey struct{}

// New builds a Proxy over pool using strategy. metrics may be nil.
func New(pool *balancer.Pool, strategy balancer.Strategy, opts Options, logger *slog.Logger, m *metrics.Metrics) *Proxy {
	p := &Proxy{
		pool:     pool,
		strategy: strategy,
		opts:     opts,
		log:      logger,
		metrics:  m,
		proxies:  make(map[*balancer.Backend]*httputil.ReverseProxy),
	}
	for _, b := range pool.All() {
		p.proxies[b] = newReverseProxy(b, logger)
	}
	return p
}

// ServeHTTP selects a backend and forwards the request, retrying on another
// healthy backend if the upstream fails before any bytes reach the client.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if p.opts.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.opts.RequestTimeout)
		defer cancel()
	}

	body, retryable := p.bufferBody(r)
	maxAttempts := 1
	if retryable {
		maxAttempts = 1 + p.opts.MaxRetries
	}

	start := time.Now()
	tried := make(map[*balancer.Backend]bool)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		backend := p.pick(r, tried)
		if backend == nil {
			break
		}
		tried[backend] = true
		if attempt > 0 {
			p.metrics.IncRetry()
		}
		if p.serveAttempt(w, r, ctx, backend, body, start) {
			if attempt > 0 {
				p.metrics.IncFailover("recovered")
			}
			return
		}
	}

	// No attempt delivered a response.
	if len(tried) > 1 {
		p.metrics.IncFailover("exhausted")
	}
	if len(tried) == 0 {
		p.log.Warn("no available backends", "method", r.Method, "path", r.URL.Path)
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}
	http.Error(w, "Bad Gateway", http.StatusBadGateway)
}

// serveAttempt forwards the request to backend once and reports whether the
// orchestration loop should stop (a response was delivered, or the failure is
// not retryable). The backend's reservation is released on return.
func (p *Proxy) serveAttempt(w http.ResponseWriter, r *http.Request, ctx context.Context, backend *balancer.Backend, body []byte, start time.Time) (stop bool) {
	defer backend.Release()

	if body != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	a := &attemptState{}
	areq := r.WithContext(context.WithValue(ctx, attemptCtxKey{}, a))
	cw := &capturingResponseWriter{ResponseWriter: w}

	p.proxies[backend].ServeHTTP(cw, areq)
	p.metrics.SetActive(backend.String(), backend.ActiveConns())

	if a.err == nil {
		status := cw.status
		if status == 0 {
			status = http.StatusOK
		}
		p.metrics.ObserveRequest(backend.String(), r.Method, status, time.Since(start).Seconds())
		if status >= 500 {
			p.recordFailure(backend)
		} else {
			p.recordSuccess(backend)
		}
		p.log.Debug("request served",
			"method", r.Method, "path", r.URL.Path,
			"backend", backend.String(), "status", status,
			"duration_ms", time.Since(start).Milliseconds())
		return true
	}

	p.recordFailure(backend)
	if cw.wrote {
		// Bytes already reached the client, so we cannot fail over; the response
		// is truncated. This is inherent to streaming reverse proxies.
		p.log.Warn("upstream failed mid-response",
			"backend", backend.String(), "path", r.URL.Path, "err", a.err)
		return true
	}
	p.log.Warn("upstream attempt failed",
		"backend", backend.String(), "method", r.Method, "path", r.URL.Path, "err", a.err)
	return false
}

// pick filters the healthy set (excluding already-tried and at-capacity
// backends) and asks the strategy to choose one, which reserves it.
func (p *Proxy) pick(r *http.Request, tried map[*balancer.Backend]bool) *balancer.Backend {
	healthy := p.pool.Healthy()

	// Fast path: first attempt with no capacity cap needs no filtering/allocation.
	candidates := healthy
	if len(tried) > 0 || p.opts.MaxInFlightPerBackend > 0 {
		candidates = candidates[:0:0]
		cap64 := int64(p.opts.MaxInFlightPerBackend)
		for _, b := range healthy {
			if tried[b] {
				continue
			}
			if p.opts.MaxInFlightPerBackend > 0 && b.ActiveConns() >= cap64 {
				continue
			}
			candidates = append(candidates, b)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	backend, err := p.strategy.Next(r, candidates)
	if err != nil {
		return nil
	}
	return backend
}

// recordSuccess clears a backend's passive-failure streak.
func (p *Proxy) recordSuccess(b *balancer.Backend) { b.RecordSuccess() }

// recordFailure advances a backend's passive-failure streak and, once it reaches
// the configured threshold, passively marks the backend unhealthy.
func (p *Proxy) recordFailure(b *balancer.Backend) {
	fails := b.RecordFailure()
	if p.opts.PassiveMaxFails > 0 && fails >= int64(p.opts.PassiveMaxFails) {
		if p.pool.SetHealthy(b, false) {
			p.log.Warn("backend passively marked down",
				"backend", b.String(), "consecutive_failures", fails)
			p.metrics.SetBackendUp(b.String(), false)
		}
	}
}

// bufferBody reads the request body into memory when the request is eligible for
// retry (idempotent method or retry_non_idempotent, retries enabled, body within
// cap), so it can be replayed on each attempt. It returns the buffered bytes (nil
// when not buffered) and whether the request is retryable.
func (p *Proxy) bufferBody(r *http.Request) (body []byte, retryable bool) {
	if p.opts.MaxRetries <= 0 || !p.isRetryableMethod(r.Method) {
		return nil, false
	}
	if r.Body == nil || r.Body == http.NoBody {
		return nil, true // nothing to replay, but still retryable
	}
	buf, err := io.ReadAll(io.LimitReader(r.Body, maxRetryBodyBytes+1))
	_ = r.Body.Close()
	if err != nil || len(buf) > maxRetryBodyBytes {
		// Too large or unreadable to safely replay: forward once with what we have.
		r.Body = io.NopCloser(bytes.NewReader(buf))
		return nil, false
	}
	return buf, true
}

// isRetryableMethod reports whether a request may be retried on another backend.
// By default only idempotent methods qualify; retry_non_idempotent lifts that.
func (p *Proxy) isRetryableMethod(method string) bool {
	if p.opts.RetryNonIdempotent {
		return true
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace,
		http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

// newReverseProxy builds a single-host ReverseProxy for one backend. Upstream
// failures and gateway-class statuses are recorded into the per-attempt state so
// the orchestrator can fail over; only when there is no attempt state (defensive)
// does it write a 502 itself.
func newReverseProxy(b *balancer.Backend, logger *slog.Logger) *httputil.ReverseProxy {
	target := b.URL
	rp := httputil.NewSingleHostReverseProxy(target)

	base := rp.Director
	rp.Director = func(r *http.Request) {
		base(r)
		r.Host = target.Host
	}

	rp.ModifyResponse = func(resp *http.Response) error {
		switch resp.StatusCode {
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return errRetryableStatus
		}
		return nil
	}

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if a, ok := r.Context().Value(attemptCtxKey{}).(*attemptState); ok {
			a.err = err
			return
		}
		logger.Error("upstream request failed", "backend", target.String(), "err", err)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}

	return rp
}

// capturingResponseWriter tracks whether any bytes were written to the client
// (so the orchestrator knows if a failover is still possible) and the status
// code that was sent. It preserves flushing/hijacking via Unwrap.
type capturingResponseWriter struct {
	http.ResponseWriter
	wrote  bool
	status int
}

func (c *capturingResponseWriter) WriteHeader(code int) {
	if !c.wrote {
		c.status = code
		c.wrote = true
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *capturingResponseWriter) Write(b []byte) (int, error) {
	if !c.wrote {
		c.status = http.StatusOK
		c.wrote = true
	}
	return c.ResponseWriter.Write(b)
}

// Unwrap exposes the underlying ResponseWriter to http.ResponseController so the
// ReverseProxy can still flush streaming responses.
func (c *capturingResponseWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
