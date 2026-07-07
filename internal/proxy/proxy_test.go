package proxy

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dylanpatriarchi/fulcrum/internal/balancer"
	"github.com/dylanpatriarchi/fulcrum/internal/config"
)

// discardLogger returns a logger that writes nowhere, keeping test output clean.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestProxy builds a round-robin Proxy over the given backend URLs and
// returns it together with the pool so tests can flip backend health.
func newTestProxy(t *testing.T, urls ...string) (*Proxy, *balancer.Pool) {
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
	strategy, err := balancer.New("round-robin")
	if err != nil {
		t.Fatalf("New strategy: %v", err)
	}
	return New(pool, strategy, discardLogger()), pool
}

func TestProxy_ForwardsRequestAndResponse(t *testing.T) {
	var (
		gotMethod, gotPath, gotQuery, gotHeader, gotBody string
	)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		gotHeader = r.Header.Get("X-Test")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("X-Upstream", "hit")
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "hello from upstream")
	}))
	defer backend.Close()

	p, _ := newTestProxy(t, backend.URL)
	front := httptest.NewServer(p)
	defer front.Close()

	req, err := http.NewRequest(http.MethodPost, front.URL+"/api/v1/thing?q=42", strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Test", "abc")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusTeapot)
	}
	if got := resp.Header.Get("X-Upstream"); got != "hit" {
		t.Errorf("X-Upstream = %q, want hit", got)
	}
	if string(body) != "hello from upstream" {
		t.Errorf("body = %q", body)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/thing" || gotQuery != "q=42" {
		t.Errorf("upstream saw %s %s?%s", gotMethod, gotPath, gotQuery)
	}
	if gotHeader != "abc" {
		t.Errorf("upstream X-Test = %q, want abc", gotHeader)
	}
	if gotBody != "payload" {
		t.Errorf("upstream body = %q, want payload", gotBody)
	}
}

func TestProxy_SetsXForwardedFor(t *testing.T) {
	var xff string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		xff = r.Header.Get("X-Forwarded-For")
	}))
	defer backend.Close()

	p, _ := newTestProxy(t, backend.URL)
	front := httptest.NewServer(p)
	defer front.Close()

	resp, err := http.Get(front.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if xff == "" {
		t.Error("X-Forwarded-For was not set by the proxy")
	}
}

func TestProxy_BadGatewayOnDeadBackend(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	dead := "http://" + l.Addr().String()
	_ = l.Close()

	p, _ := newTestProxy(t, dead)
	front := httptest.NewServer(p)
	defer front.Close()

	resp, err := http.Get(front.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want %d (Bad Gateway)", resp.StatusCode, http.StatusBadGateway)
	}
}

func TestProxy_ServiceUnavailableWhenNoHealthyBackends(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()

	p, pool := newTestProxy(t, backend.URL)
	// Mark every backend unhealthy (through the pool so the snapshot refreshes).
	for _, b := range pool.All() {
		pool.SetHealthy(b, false)
	}

	front := httptest.NewServer(p)
	defer front.Close()

	resp, err := http.Get(front.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d (Service Unavailable)", resp.StatusCode, http.StatusServiceUnavailable)
	}
}

func TestProxy_DistributesAcrossBackends(t *testing.T) {
	hits := make([]int, 2)
	mk := func(i int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits[i]++
		}))
	}
	b0, b1 := mk(0), mk(1)
	defer b0.Close()
	defer b1.Close()

	p, _ := newTestProxy(t, b0.URL, b1.URL)
	front := httptest.NewServer(p)
	defer front.Close()

	const n = 10
	for i := 0; i < n; i++ {
		resp, err := http.Get(front.URL + "/")
		if err != nil {
			t.Fatalf("get #%d: %v", i, err)
		}
		resp.Body.Close()
	}
	if hits[0] != n/2 || hits[1] != n/2 {
		t.Errorf("round-robin distribution = %v, want [%d %d]", hits, n/2, n/2)
	}
}

func TestProxy_ReleasesActiveConns(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()

	p, pool := newTestProxy(t, backend.URL)
	front := httptest.NewServer(p)
	defer front.Close()

	for i := 0; i < 5; i++ {
		resp, err := http.Get(front.URL + "/")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		resp.Body.Close()
	}
	// After all requests complete, the active-conn counter must be back to zero.
	if got := pool.All()[0].ActiveConns(); got != 0 {
		t.Errorf("ActiveConns() = %d, want 0 after requests drained", got)
	}
}

func TestNewServer_ServesAndShutsDown(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	cfg := &config.Config{
		Listen:   "127.0.0.1:0",
		Strategy: "round-robin",
		Backends: []config.Backend{{URL: backend.URL, Weight: 1}},
	}
	srv, err := NewServer(cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(l) }()

	resp, err := http.Get("http://" + l.Addr().String() + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" {
		t.Errorf("body = %q, want ok", body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := <-serveErr; err != nil && err != http.ErrServerClosed {
		t.Errorf("Serve returned %v, want ErrServerClosed", err)
	}
}

func TestNewServer_NoBackends(t *testing.T) {
	_, err := NewServer(&config.Config{Listen: ":8080", Strategy: "round-robin"}, discardLogger())
	if err == nil {
		t.Fatal("NewServer with no backends = nil error, want error")
	}
}

func TestNewServer_UnknownStrategy(t *testing.T) {
	cfg := &config.Config{
		Listen:   ":8080",
		Strategy: "nonexistent",
		Backends: []config.Backend{{URL: "http://a.com", Weight: 1}},
	}
	_, err := NewServer(cfg, discardLogger())
	if err == nil {
		t.Fatal("NewServer with unknown strategy = nil error, want error")
	}
}
