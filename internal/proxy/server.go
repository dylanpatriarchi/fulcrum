package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/dylanpatriarchi/fulcrum/internal/config"
)

// Server wires a configuration into a runnable HTTP reverse-proxy server.
type Server struct {
	http *http.Server
	log  *slog.Logger
}

// NewServer builds a Server from cfg.
//
// Milestone 1 forwards to the first configured backend only; the pool- and
// strategy-aware handler arrives in Milestone 2.
func NewServer(cfg *config.Config, logger *slog.Logger) (*Server, error) {
	if len(cfg.Backends) == 0 {
		return nil, fmt.Errorf("proxy: no backends configured")
	}
	target, err := url.Parse(cfg.Backends[0].URL)
	if err != nil {
		return nil, fmt.Errorf("proxy: parse backend url %q: %w", cfg.Backends[0].URL, err)
	}

	srv := &http.Server{
		Addr:    cfg.Listen,
		Handler: SingleBackend(target, logger),
		// Guard against slowloris-style header stalls; full per-request timeouts
		// land in Milestone 6.
		ReadHeaderTimeout: 10 * time.Second,
	}
	return &Server{http: srv, log: logger}, nil
}

// ListenAndServe starts serving on the configured listen address and blocks
// until the server is shut down. It returns http.ErrServerClosed on a clean
// Shutdown, which callers should treat as success.
func (s *Server) ListenAndServe() error {
	s.log.Info("fulcrum listening", "addr", s.http.Addr)
	return s.http.ListenAndServe()
}

// Serve serves on an already-open listener. Useful for tests that need the
// bound address (e.g. a :0 ephemeral port).
func (s *Server) Serve(l net.Listener) error {
	s.log.Info("fulcrum listening", "addr", l.Addr().String())
	return s.http.Serve(l)
}

// Shutdown gracefully drains in-flight requests, bounded by ctx.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}
