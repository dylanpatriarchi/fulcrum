// Package proxy implements Fulcrum's HTTP reverse proxy.
//
// Milestone 1 provides a single-backend reverse proxy. Later milestones add a
// backend Pool, pluggable balancing strategies, health checking and failover.
package proxy

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// SingleBackend returns an http.Handler that reverse-proxies every request to
// target. On upstream failure it responds 502 Bad Gateway rather than leaking
// the raw transport error to the client.
func SingleBackend(target *url.URL, logger *slog.Logger) http.Handler {
	rp := httputil.NewSingleHostReverseProxy(target)

	// Preserve the default director (host/scheme/path rewrite + X-Forwarded-For)
	// but wrap it so the upstream sees a sane Host header for name-based vhosts.
	base := rp.Director
	rp.Director = func(r *http.Request) {
		base(r)
		r.Host = target.Host
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
