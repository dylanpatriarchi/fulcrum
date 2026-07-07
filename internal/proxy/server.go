package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/dylanpatriarchi/fulcrum/internal/balancer"
	"github.com/dylanpatriarchi/fulcrum/internal/config"
	"github.com/dylanpatriarchi/fulcrum/internal/health"
	"github.com/dylanpatriarchi/fulcrum/internal/metrics"
)

// Server wires a configuration into a runnable HTTP reverse-proxy server, plus
// an optional admin server exposing metrics, liveness and per-backend stats.
type Server struct {
	proxy   *http.Server
	admin   *http.Server // nil when admin.listen is empty
	checker *health.Checker
	pool    *balancer.Pool
	metrics *metrics.Metrics
	log     *slog.Logger
}

// NewServer builds a Server from cfg.
func NewServer(cfg *config.Config, logger *slog.Logger) (*Server, error) {
	pool, err := poolFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	strategy, err := balancer.New(cfg.Strategy, balancer.Options{
		TrustForwardedHeaders: cfg.Proxy.TrustForwardedHeaders,
	})
	if err != nil {
		return nil, fmt.Errorf("proxy: %w", err)
	}

	m := metrics.New()
	for _, b := range pool.All() {
		m.SetBackendUp(b.String(), b.Healthy())
		m.SetActive(b.String(), b.ActiveConns())
	}

	handler := New(pool, strategy, Options{
		MaxRetries:            cfg.Proxy.MaxRetries,
		RetryNonIdempotent:    cfg.Proxy.RetryNonIdempotent,
		MaxInFlightPerBackend: cfg.Proxy.MaxInFlightPerBackend,
		PassiveMaxFails:       cfg.Proxy.PassiveMaxFails,
		RequestTimeout:        cfg.Proxy.RequestTimeout.Std(),
	}, logger, m)

	checker := health.NewChecker(pool, health.Options{
		Path:               cfg.HealthCheck.Path,
		Interval:           cfg.HealthCheck.Interval.Std(),
		Timeout:            cfg.HealthCheck.Timeout.Std(),
		HealthyThreshold:   cfg.HealthCheck.HealthyThreshold,
		UnhealthyThreshold: cfg.HealthCheck.UnhealthyThreshold,
	}, logger, m)

	s := &Server{
		proxy: &http.Server{
			Addr:              cfg.Listen,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second, // slowloris guard
			IdleTimeout:       60 * time.Second,
		},
		checker: checker,
		pool:    pool,
		metrics: m,
		log:     logger,
	}
	if cfg.Admin.Listen != "" {
		s.admin = &http.Server{
			Addr:              cfg.Admin.Listen,
			Handler:           s.adminHandler(),
			ReadHeaderTimeout: 10 * time.Second,
		}
	}
	return s, nil
}

// adminHandler serves /metrics, /healthz and /stats.
func (s *Server) adminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", s.metrics.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		type backendStats struct {
			URL string `json:"url"`
			balancer.Stats
		}
		all := s.pool.All()
		out := make([]backendStats, 0, len(all))
		for _, b := range all {
			out = append(out, backendStats{URL: b.String(), Stats: b.Stats()})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	return mux
}

// StartBackground launches the health checker and the admin server (if enabled).
// Both stop when ctx is cancelled. It returns immediately.
func (s *Server) StartBackground(ctx context.Context) {
	go s.checker.Run(ctx)
	if s.admin == nil {
		return
	}
	go func() {
		s.log.Info("admin server listening", "addr", s.admin.Addr)
		if err := s.admin.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.log.Error("admin server failed", "err", err)
		}
	}()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.admin.Shutdown(shutdownCtx)
	}()
}

// poolFromConfig builds the backend pool from configuration. config.Validate has
// already guaranteed every URL is well-formed, so NewBackend cannot fail here for
// a validated config.
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
	s.log.Info("fulcrum listening", "addr", s.proxy.Addr)
	return s.proxy.ListenAndServe()
}

// Serve serves on an already-open listener. Useful for tests that need the bound
// address (e.g. a :0 ephemeral port).
func (s *Server) Serve(l net.Listener) error {
	s.log.Info("fulcrum listening", "addr", l.Addr().String())
	return s.proxy.Serve(l)
}

// Shutdown gracefully drains in-flight requests on both servers, bounded by ctx.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.admin != nil {
		_ = s.admin.Shutdown(ctx)
	}
	return s.proxy.Shutdown(ctx)
}
