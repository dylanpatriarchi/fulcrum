package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dylanpatriarchi/fulcrum/internal/config"
)

func TestServer_AdminEndpoints(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer backend.Close()

	cfg := &config.Config{
		Listen:   "127.0.0.1:0",
		Strategy: "round-robin",
		Backends: []config.Backend{{URL: backend.URL, Weight: 1}},
		Admin:    config.Admin{Listen: "127.0.0.1:0"},
	}
	cfg.HealthCheck.Path = "/healthz"
	srv, err := NewServer(cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// Drive one request through the proxy handler to populate metrics/stats.
	front := httptest.NewServer(srv.proxy.Handler)
	defer front.Close()
	resp, err := http.Get(front.URL + "/")
	if err != nil {
		t.Fatalf("proxy get: %v", err)
	}
	resp.Body.Close()

	admin := httptest.NewServer(srv.adminHandler())
	defer admin.Close()

	t.Run("healthz", func(t *testing.T) {
		resp, err := http.Get(admin.URL + "/healthz")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || string(body) != "ok" {
			t.Errorf("/healthz = %d %q, want 200 ok", resp.StatusCode, body)
		}
	})

	t.Run("metrics", func(t *testing.T) {
		resp, err := http.Get(admin.URL + "/metrics")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "fulcrum_requests_total") {
			t.Errorf("/metrics missing fulcrum_requests_total; got:\n%s", truncate(string(body), 400))
		}
		if !strings.Contains(string(body), "fulcrum_backend_up") {
			t.Error("/metrics missing fulcrum_backend_up")
		}
	})

	t.Run("stats", func(t *testing.T) {
		resp, err := http.Get(admin.URL + "/stats")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		defer resp.Body.Close()
		var stats []struct {
			URL           string `json:"url"`
			Healthy       bool   `json:"healthy"`
			TotalRequests int64  `json:"total_requests"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
			t.Fatalf("decode /stats: %v", err)
		}
		if len(stats) != 1 {
			t.Fatalf("stats len = %d, want 1", len(stats))
		}
		if stats[0].URL != backend.URL {
			t.Errorf("stats url = %q, want %q", stats[0].URL, backend.URL)
		}
		if stats[0].TotalRequests < 1 {
			t.Errorf("stats total_requests = %d, want >= 1", stats[0].TotalRequests)
		}
	})
}

func TestServer_AdminDisabledWhenListenEmpty(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()

	cfg := &config.Config{
		Listen:   "127.0.0.1:0",
		Strategy: "round-robin",
		Backends: []config.Backend{{URL: backend.URL, Weight: 1}},
		Admin:    config.Admin{Listen: ""},
	}
	srv, err := NewServer(cfg, discardLogger())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if srv.admin != nil {
		t.Error("admin server should be nil when admin.listen is empty")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
