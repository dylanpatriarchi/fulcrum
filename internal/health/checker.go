// Package health implements active health checking: a background checker probes
// each backend on an interval and flips its health state with hysteresis (N
// consecutive successes/failures required to change state), so a single flaky
// probe cannot make a backend flap in and out of rotation.
package health

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/dylanpatriarchi/fulcrum/internal/balancer"
)

// Options configures the active health checker.
type Options struct {
	Path               string        // request path probed on each backend
	Interval           time.Duration // time between probe rounds
	Timeout            time.Duration // per-probe timeout
	HealthyThreshold   int           // consecutive successes to mark a backend UP
	UnhealthyThreshold int           // consecutive failures to mark a backend DOWN
}

// backendState tracks the consecutive-result streaks for one backend. Each
// backend's state is only touched by the single goroutine handling it within a
// probe round, and rounds never overlap, so no per-state locking is needed.
type backendState struct {
	consecSuccess int
	consecFail    int
}

// Checker periodically probes the pool's backends and updates their health.
type Checker struct {
	pool   *balancer.Pool
	client *http.Client
	opts   Options
	log    *slog.Logger

	// state is populated once at construction (keyed by the stable *Backend
	// pointers) and only its values mutate afterwards, so concurrent reads of
	// the map are safe without locking.
	state map[*balancer.Backend]*backendState
}

// NewChecker builds a Checker over pool. The pool's backend set is captured at
// construction (it is fixed for the process lifetime).
func NewChecker(pool *balancer.Pool, opts Options, logger *slog.Logger) *Checker {
	state := make(map[*balancer.Backend]*backendState)
	for _, b := range pool.All() {
		state[b] = &backendState{}
	}
	return &Checker{
		pool: pool,
		// No redirects: a 3xx to a healthy-looking page must not mask a sick
		// backend. The per-probe timeout is enforced via context, but we also set
		// a client timeout as a backstop.
		client: &http.Client{
			Timeout: opts.Timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		opts:  opts,
		log:   logger,
		state: state,
	}
}

// Run probes immediately, then on every interval tick, until ctx is cancelled.
func (c *Checker) Run(ctx context.Context) {
	ticker := time.NewTicker(c.opts.Interval)
	defer ticker.Stop()

	c.CheckOnce(ctx) // fail/verify fast at startup instead of waiting one interval
	for {
		select {
		case <-ctx.Done():
			c.log.Info("health checker stopping")
			return
		case <-ticker.C:
			c.CheckOnce(ctx)
		}
	}
}

// CheckOnce probes every backend once, concurrently, and records the outcome.
// It is exported so tests can drive rounds deterministically without timing.
func (c *Checker) CheckOnce(ctx context.Context) {
	var wg sync.WaitGroup
	for b := range c.state {
		wg.Add(1)
		go func(b *balancer.Backend) {
			defer wg.Done()
			c.record(b, c.probe(ctx, b))
		}(b)
	}
	wg.Wait()
}

// probe performs a single health request and reports whether it succeeded
// (a 2xx response within the timeout).
func (c *Checker) probe(ctx context.Context, b *balancer.Backend) bool {
	ctx, cancel := context.WithTimeout(ctx, c.opts.Timeout)
	defer cancel()

	target := b.URL.JoinPath(c.opts.Path).String()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body) // drain to allow connection reuse
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// record advances the backend's streak counters and flips its health state once
// the relevant threshold is reached (hysteresis).
func (c *Checker) record(b *balancer.Backend, ok bool) {
	s := c.state[b]
	if ok {
		s.consecFail = 0
		if s.consecSuccess < c.opts.HealthyThreshold {
			s.consecSuccess++
		}
		// Route the flip through the pool so its cached healthy snapshot is
		// rebuilt and the backend re-enters rotation.
		if !b.Healthy() && s.consecSuccess >= c.opts.HealthyThreshold {
			if c.pool.SetHealthy(b, true) {
				c.log.Info("backend restored to rotation", "backend", b.String())
			}
		}
		return
	}

	s.consecSuccess = 0
	if s.consecFail < c.opts.UnhealthyThreshold {
		s.consecFail++
	}
	if b.Healthy() && s.consecFail >= c.opts.UnhealthyThreshold {
		if c.pool.SetHealthy(b, false) {
			c.log.Warn("backend removed from rotation", "backend", b.String())
		}
	}
}
