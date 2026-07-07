// Package proxy implements Fulcrum's HTTP reverse proxy: it selects a backend
// via the configured balancing strategy and forwards the request to it.
//
// Milestone 2 adds multi-backend forwarding through a Pool + Strategy. Bounded
// retry/failover and passive health marking arrive in later milestones; for now
// an upstream failure surfaces as 502 to the client.
package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httputil"

	"github.com/dylanpatriarchi/fulcrum/internal/balancer"
)

// Proxy forwards each request to a backend chosen by its Strategy from the
// Pool's currently healthy set.
type Proxy struct {
	pool     *balancer.Pool
	strategy balancer.Strategy
	log      *slog.Logger

	// proxies holds one ReverseProxy per backend, built once at construction.
	// The map is read-only after New returns, so it needs no synchronisation.
	proxies map[*balancer.Backend]*httputil.ReverseProxy
}

// New builds a Proxy over pool using strategy. It constructs one ReverseProxy
// per backend up front.
func New(pool *balancer.Pool, strategy balancer.Strategy, logger *slog.Logger) *Proxy {
	p := &Proxy{
		pool:     pool,
		strategy: strategy,
		log:      logger,
		proxies:  make(map[*balancer.Backend]*httputil.ReverseProxy),
	}
	for _, b := range pool.All() {
		p.proxies[b] = newReverseProxy(b, logger)
	}
	return p
}

// ServeHTTP selects a healthy backend and proxies the request to it. If no
// backend is healthy it responds 503; if the chosen upstream errors it responds
// 502 (failover is added in a later milestone).
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	backend, err := p.strategy.Next(r, p.pool.Healthy())
	if err != nil {
		p.log.Warn("no healthy backends", "method", r.Method, "path", r.URL.Path, "err", err)
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	rp := p.proxies[backend]
	if rp == nil {
		// Defensive: a healthy backend with no ReverseProxy means it was not part
		// of the pool at construction, which should be impossible.
		p.log.Error("no reverse proxy for backend", "backend", backend.String())
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	backend.Acquire()
	defer backend.Release()
	rp.ServeHTTP(w, r)
}

// newReverseProxy builds a single-host ReverseProxy for one backend. On upstream
// failure it responds 502 rather than leaking the transport error.
func newReverseProxy(b *balancer.Backend, logger *slog.Logger) *httputil.ReverseProxy {
	target := b.URL
	rp := httputil.NewSingleHostReverseProxy(target)

	base := rp.Director
	rp.Director = func(r *http.Request) {
		base(r)
		r.Host = target.Host // sane Host header for name-based vhosts
	}

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		logger.Error("upstream request failed",
			"backend", target.String(),
			"method", r.Method,
			"path", r.URL.Path,
			"err", err,
		)
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}

	return rp
}
