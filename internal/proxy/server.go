package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/dylanpatriarchi/fulcrum/internal/balancer"
	"github.com/dylanpatriarchi/fulcrum/internal/config"
)

// Server wires a configuration into a runnable HTTP reverse-proxy server.
type Server struct {
	http *http.Server
	log  *slog.Logger
}

// NewServer builds a Server from cfg: it constructs the backend pool, the
// configured strategy and the balancing proxy handler.
func NewServer(cfg *config.Config, logger *slog.Logger) (*Server, error) {
	pool, err := poolFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	strategy, err := balancer.New(cfg.Strategy)
	if err != nil {
		return nil, fmt.Errorf("proxy: %w", err)
	}

	handler := New(pool, strategy, logger)
	srv := &http.Server{
		Addr:    cfg.Listen,
		Handler: handler,
		// Guard against slowloris-style header stalls; full per-request timeouts
		// land in a later milestone.
		ReadHeaderTimeout: 10 * time.Second,
	}
	return &Server{http: srv, log: logger}, nil
}

// poolFromConfig builds the backend pool from configuration, parsing each URL
// exactly once (config.Validate already guaranteed they are well-formed).
func poolFromConfig(cfg *config.Config) (*balancer.Pool, error) {
	if len(cfg.Backends) == 0 {
		return nil, fmt.Errorf("proxy: no backends configured")
	}
	backends := make([]*balancer.Backend, 0, len(cfg.Backends))
	for _, b := range cfg.Backends {
		backend, err := balancer.NewBackend(b.URL, b.Weight)
		if err != nil {
			return nil, fmt.Errorf("proxy: %w", err)
		}
		backends = append(backends, backend)
	}
	return balancer.NewPool(backends), nil
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
