package health

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dylanpatriarchi/fulcrum/internal/balancer"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// flakyBackend is an httptest server whose response status can be switched at
// runtime so a test can drive a backend sick and healthy again.
type flakyBackend struct {
	srv  *httptest.Server
	code atomic.Int64
}

func newFlakyBackend() *flakyBackend {
	fb := &flakyBackend{}
	fb.code.Store(http.StatusOK)
	fb.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(fb.code.Load()))
	}))
	return fb
}

func (fb *flakyBackend) setCode(c int) { fb.code.Store(int64(c)) }
func (fb *flakyBackend) close()        { fb.srv.Close() }

func newChecker(t *testing.T, pool *balancer.Pool, healthy, unhealthy int) *Checker {
	t.Helper()
	return NewChecker(pool, Options{
		Path:               "/",
		Interval:           time.Hour, // tests drive CheckOnce directly
		Timeout:            time.Second,
		HealthyThreshold:   healthy,
		UnhealthyThreshold: unhealthy,
	}, discardLogger(), nil)
}

func assertHealth(t *testing.T, b *balancer.Backend, want bool, when string) {
	t.Helper()
	if b.Healthy() != want {
		t.Fatalf("%s: Healthy() = %v, want %v", when, b.Healthy(), want)
	}
}

// TestChecker_Hysteresis is the core M4 test: a backend must require N
// consecutive failures to leave rotation and M consecutive successes to
// re-enter, and interleaved opposite results reset the streak.
func TestChecker_Hysteresis(t *testing.T) {
	fb := newFlakyBackend()
	defer fb.close()

	b, err := balancer.NewBackend(fb.srv.URL, 1)
	if err != nil {
		t.Fatalf("NewBackend: %v", err)
	}
	pool := balancer.NewPool([]*balancer.Backend{b})
	c := newChecker(t, pool, 2 /*healthy*/, 3 /*unhealthy*/)
	ctx := context.Background()

	assertHealth(t, b, true, "startup")

	// Failing: 3 consecutive failures required to flip DOWN.
	fb.setCode(http.StatusInternalServerError)
	c.CheckOnce(ctx)
	assertHealth(t, b, true, "after 1 failure")
	c.CheckOnce(ctx)
	assertHealth(t, b, true, "after 2 failures")
	c.CheckOnce(ctx)
	assertHealth(t, b, false, "after 3 failures")
	if got := len(pool.Healthy()); got != 0 {
		t.Errorf("down backend still in rotation: Healthy() len = %d, want 0", got)
	}

	// One success is not enough (needs 2) — and a failure resets the streak.
	fb.setCode(http.StatusOK)
	c.CheckOnce(ctx)
	assertHealth(t, b, false, "after 1 success")
	fb.setCode(http.StatusInternalServerError)
	c.CheckOnce(ctx)
	assertHealth(t, b, false, "failure resets success streak")

	// Two clean successes bring it back.
	fb.setCode(http.StatusOK)
	c.CheckOnce(ctx)
	assertHealth(t, b, false, "after 1 success (post-reset)")
	c.CheckOnce(ctx)
	assertHealth(t, b, true, "after 2 successes")
	if got := len(pool.Healthy()); got != 1 {
		t.Errorf("restored backend not in rotation: Healthy() len = %d, want 1", got)
	}
}

// TestChecker_UnhealthyThresholdOne flips immediately when threshold is 1.
func TestChecker_UnhealthyThresholdOne(t *testing.T) {
	fb := newFlakyBackend()
	defer fb.close()
	b, _ := balancer.NewBackend(fb.srv.URL, 1)
	pool := balancer.NewPool([]*balancer.Backend{b})
	c := newChecker(t, pool, 1, 1)
	ctx := context.Background()

	fb.setCode(http.StatusBadGateway)
	c.CheckOnce(ctx)
	assertHealth(t, b, false, "one failure with threshold 1")

	fb.setCode(http.StatusOK)
	c.CheckOnce(ctx)
	assertHealth(t, b, true, "one success with threshold 1")
}

// TestChecker_ProbeTimeout treats a too-slow backend as a failure.
func TestChecker_ProbeTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()

	b, _ := balancer.NewBackend(slow.URL, 1)
	pool := balancer.NewPool([]*balancer.Backend{b})
	c := NewChecker(pool, Options{
		Path:               "/",
		Interval:           time.Hour,
		Timeout:            50 * time.Millisecond,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
	}, discardLogger(), nil)

	c.CheckOnce(context.Background())
	assertHealth(t, b, false, "slow backend beyond timeout")
}

// TestChecker_ConnectionRefused treats an unreachable backend as a failure.
func TestChecker_ConnectionRefused(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	dead := "http://" + l.Addr().String()
	_ = l.Close()

	b, _ := balancer.NewBackend(dead, 1)
	pool := balancer.NewPool([]*balancer.Backend{b})
	c := newChecker(t, pool, 1, 1)

	c.CheckOnce(context.Background())
	assertHealth(t, b, false, "connection refused")
}

// TestChecker_IndependentBackends verifies one backend going down does not
// affect a healthy sibling, and only the sick one leaves rotation.
func TestChecker_IndependentBackends(t *testing.T) {
	good := newFlakyBackend()
	defer good.close()
	bad := newFlakyBackend()
	defer bad.close()

	bg, _ := balancer.NewBackend(good.srv.URL, 1)
	bb, _ := balancer.NewBackend(bad.srv.URL, 1)
	pool := balancer.NewPool([]*balancer.Backend{bg, bb})
	c := newChecker(t, pool, 1, 2)
	ctx := context.Background()

	bad.setCode(http.StatusInternalServerError)
	c.CheckOnce(ctx)
	c.CheckOnce(ctx)

	assertHealth(t, bg, true, "healthy sibling")
	assertHealth(t, bb, false, "sick backend")
	healthy := pool.Healthy()
	if len(healthy) != 1 || healthy[0] != bg {
		t.Errorf("rotation = %v, want only the healthy backend", healthy)
	}
}

// TestChecker_RunStopsOnContextCancel ensures Run returns when ctx is cancelled.
func TestChecker_RunStopsOnContextCancel(t *testing.T) {
	fb := newFlakyBackend()
	defer fb.close()
	b, _ := balancer.NewBackend(fb.srv.URL, 1)
	pool := balancer.NewPool([]*balancer.Backend{b})
	c := NewChecker(pool, Options{
		Path:               "/",
		Interval:           10 * time.Millisecond,
		Timeout:            time.Second,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
	}, discardLogger(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()

	time.Sleep(30 * time.Millisecond) // let a few ticks run
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
