package proxy

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dylanpatriarchi/fulcrum/internal/balancer"
)

// buildProxy assembles a Proxy over the given backend URLs with explicit options.
func buildProxy(t *testing.T, opts Options, urls ...string) (*Proxy, *balancer.Pool) {
	t.Helper()
	backends := make([]*balancer.Backend, 0, len(urls))
	for _, u := range urls {
		b, err := balancer.NewBackend(u, 1)
		if err != nil {
			t.Fatalf("NewBackend(%q): %v", u, err)
		}
		backends = append(backends, b)
	}
	pool := balancer.NewPool(backends)
	strategy, err := balancer.New("round-robin", balancer.Options{})
	if err != nil {
		t.Fatalf("New strategy: %v", err)
	}
	return New(pool, strategy, opts, discardLogger(), nil), pool
}

// deadBackendURL returns a URL that refuses connections.
func deadBackendURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	url := "http://" + l.Addr().String()
	_ = l.Close()
	return url
}

// TestProxy_FailoverReroutesAroundFailingBackend is the credibility test: with a
// failing backend and a healthy one, no request should leak a 5xx to the client
// within the retry budget.
func TestProxy_FailoverReroutesAroundFailingBackend(t *testing.T) {
	var goodHits atomic.Int64
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		goodHits.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	defer good.Close()

	// A backend that always returns 503 (a gateway-class status the proxy retries).
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()

	p, _ := buildProxy(t, Options{MaxRetries: 1}, bad.URL, good.URL)
	front := httptest.NewServer(p)
	defer front.Close()

	const n = 20
	for i := 0; i < n; i++ {
		resp, err := http.Get(front.URL + "/")
		if err != nil {
			t.Fatalf("get #%d: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 500 {
			t.Fatalf("request #%d leaked a 5xx (%d) to the client", i, resp.StatusCode)
		}
		if resp.StatusCode != http.StatusOK || string(body) != "ok" {
			t.Fatalf("request #%d = %d %q, want 200 ok", i, resp.StatusCode, body)
		}
	}
	if goodHits.Load() < n {
		t.Errorf("healthy backend served %d requests, want >= %d", goodHits.Load(), n)
	}
}

// TestProxy_FailoverAroundDeadBackend covers a connection-level failure (refused).
func TestProxy_FailoverAroundDeadBackend(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer good.Close()
	dead := deadBackendURL(t)

	p, _ := buildProxy(t, Options{MaxRetries: 1}, dead, good.URL)
	front := httptest.NewServer(p)
	defer front.Close()

	for i := 0; i < 10; i++ {
		resp, err := http.Get(front.URL + "/")
		if err != nil {
			t.Fatalf("get #%d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request #%d = %d, want 200 (failover around dead backend)", i, resp.StatusCode)
		}
	}
}

// TestProxy_AllBackendsFailingReturns502 verifies the client gets a single clean
// 502 when every backend fails within the retry budget.
func TestProxy_AllBackendsFailingReturns502(t *testing.T) {
	mk := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
	}
	b0, b1 := mk(), mk()
	defer b0.Close()
	defer b1.Close()

	p, _ := buildProxy(t, Options{MaxRetries: 2}, b0.URL, b1.URL)
	front := httptest.NewServer(p)
	defer front.Close()

	resp, err := http.Get(front.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 when all backends fail", resp.StatusCode)
	}
}

// TestProxy_NonIdempotentNotRetried ensures a POST is not replayed by default.
func TestProxy_NonIdempotentNotRetried(t *testing.T) {
	var attempts atomic.Int64
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	defer good.Close()

	// Only the "bad" backend, so a retry (if it happened) would still fail, but
	// we assert the request is attempted exactly once for a non-idempotent POST.
	p, _ := buildProxy(t, Options{MaxRetries: 3}, bad.URL)
	front := httptest.NewServer(p)
	defer front.Close()

	resp, err := http.Post(front.URL+"/", "text/plain", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if got := attempts.Load(); got != 1 {
		t.Errorf("POST attempted %d times, want 1 (non-idempotent, no retry)", got)
	}
}

// TestProxy_PassiveMarkDown verifies a repeatedly-failing backend is passively
// removed from rotation after the configured number of consecutive failures.
func TestProxy_PassiveMarkDown(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()

	// Single backend, no retries, passive threshold 2.
	p, pool := buildProxy(t, Options{MaxRetries: 0, PassiveMaxFails: 2}, bad.URL)
	front := httptest.NewServer(p)
	defer front.Close()

	// First two requests fail (502) and trip the passive threshold.
	for i := 0; i < 2; i++ {
		resp, err := http.Get(front.URL + "/")
		if err != nil {
			t.Fatalf("get #%d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("request #%d = %d, want 502", i, resp.StatusCode)
		}
	}
	if got := len(pool.Healthy()); got != 0 {
		t.Fatalf("backend still healthy after %d failures; Healthy() len = %d, want 0", 2, got)
	}
	// With no healthy backend left, the next request is shed with 503.
	resp, err := http.Get(front.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 after passive removal", resp.StatusCode)
	}
}

// TestProxy_MaxInFlightSheds verifies the per-backend concurrency cap: while a
// backend is at capacity, a further request with no other backend is shed (503).
func TestProxy_MaxInFlightSheds(t *testing.T) {
	release := make(chan struct{})
	var inFlight atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inFlight.Add(1)
		<-release // hold the connection open
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	p, _ := buildProxy(t, Options{MaxInFlightPerBackend: 1}, backend.URL)
	front := httptest.NewServer(p)
	defer front.Close()

	// Occupy the single slot with a held request.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp, err := http.Get(front.URL + "/hold")
		if err == nil {
			resp.Body.Close()
		}
	}()

	// Wait until the held request is actually in flight on the backend.
	deadline := time.Now().Add(2 * time.Second)
	for inFlight.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if inFlight.Load() == 0 {
		close(release)
		wg.Wait()
		t.Fatal("held request never reached the backend")
	}

	// A second request finds the only backend at capacity → shed with 503.
	resp, err := http.Get(front.URL + "/second")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when backend at capacity", resp.StatusCode)
	}

	close(release)
	wg.Wait()
}
