package proxy

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dylanpatriarchi/fulcrum/internal/config"
)

// discardLogger returns a logger that writes nowhere, keeping test output clean.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mustURL parses u or fails the test.
func mustURL(t *testing.T, u string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("parse url %q: %v", u, err)
	}
	return parsed
}

func TestSingleBackend_ForwardsRequestAndResponse(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotQuery  string
		gotHeader string
		gotBody   string
	)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotHeader = r.Header.Get("X-Test")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)

		w.Header().Set("X-Upstream", "hit")
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "hello from upstream")
	}))
	defer backend.Close()

	front := httptest.NewServer(SingleBackend(mustURL(t, backend.URL), discardLogger()))
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

	// Response propagated back to the client.
	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusTeapot)
	}
	if got := resp.Header.Get("X-Upstream"); got != "hit" {
		t.Errorf("X-Upstream = %q, want hit", got)
	}
	if string(body) != "hello from upstream" {
		t.Errorf("body = %q, want %q", body, "hello from upstream")
	}

	// Request forwarded faithfully.
	if gotMethod != http.MethodPost {
		t.Errorf("upstream method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/v1/thing" {
		t.Errorf("upstream path = %q, want /api/v1/thing", gotPath)
	}
	if gotQuery != "q=42" {
		t.Errorf("upstream query = %q, want q=42", gotQuery)
	}
	if gotHeader != "abc" {
		t.Errorf("upstream X-Test = %q, want abc", gotHeader)
	}
	if gotBody != "payload" {
		t.Errorf("upstream body = %q, want payload", gotBody)
	}
}

func TestSingleBackend_SetsXForwardedFor(t *testing.T) {
	var xff string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		xff = r.Header.Get("X-Forwarded-For")
	}))
	defer backend.Close()

	front := httptest.NewServer(SingleBackend(mustURL(t, backend.URL), discardLogger()))
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

func TestSingleBackend_BadGatewayOnDeadBackend(t *testing.T) {
	// Reserve a port then close it, guaranteeing a refused connection.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	dead := "http://" + l.Addr().String()
	_ = l.Close()

	front := httptest.NewServer(SingleBackend(mustURL(t, dead), discardLogger()))
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
	_, err := NewServer(&config.Config{Listen: ":8080"}, discardLogger())
	if err == nil {
		t.Fatal("NewServer with no backends = nil error, want error")
	}
}
