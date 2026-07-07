package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validYAML = `
listen: ":8080"
strategy: round-robin
backends:
  - url: "http://127.0.0.1:9001"
    weight: 1
  - url: "http://127.0.0.1:9002"
    weight: 3
health_check:
  interval: 5s
  timeout: 1s
  path: /healthz
  healthy_threshold: 2
  unhealthy_threshold: 3
proxy:
  request_timeout: 15s
  max_retries: 2
  max_in_flight_per_backend: 100
`

func TestParse_Valid(t *testing.T) {
	c, err := Parse([]byte(validYAML))
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if c.Listen != ":8080" {
		t.Errorf("Listen = %q, want :8080", c.Listen)
	}
	if c.Strategy != "round-robin" {
		t.Errorf("Strategy = %q, want round-robin", c.Strategy)
	}
	if len(c.Backends) != 2 {
		t.Fatalf("len(Backends) = %d, want 2", len(c.Backends))
	}
	if got := c.Backends[1].Weight; got != 3 {
		t.Errorf("Backends[1].Weight = %d, want 3", got)
	}
	if got := c.HealthCheck.Interval.Std(); got != 5*time.Second {
		t.Errorf("HealthCheck.Interval = %v, want 5s", got)
	}
	if got := c.Proxy.RequestTimeout.Std(); got != 15*time.Second {
		t.Errorf("Proxy.RequestTimeout = %v, want 15s", got)
	}
}

func TestParse_AppliesDefaults(t *testing.T) {
	const minimal = `
listen: ":9000"
strategy: random
backends:
  - url: "http://example.com"
`
	c, err := Parse([]byte(minimal))
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if c.Backends[0].Weight != 1 {
		t.Errorf("default Weight = %d, want 1", c.Backends[0].Weight)
	}
	if c.HealthCheck.Path != "/healthz" {
		t.Errorf("default Path = %q, want /healthz", c.HealthCheck.Path)
	}
	if c.HealthCheck.HealthyThreshold != 2 {
		t.Errorf("default HealthyThreshold = %d, want 2", c.HealthCheck.HealthyThreshold)
	}
	if c.HealthCheck.UnhealthyThreshold != 3 {
		t.Errorf("default UnhealthyThreshold = %d, want 3", c.HealthCheck.UnhealthyThreshold)
	}
	if c.HealthCheck.Interval.Std() != 10*time.Second {
		t.Errorf("default Interval = %v, want 10s", c.HealthCheck.Interval.Std())
	}
	if c.HealthCheck.Timeout.Std() != 2*time.Second {
		t.Errorf("default Timeout = %v, want 2s", c.HealthCheck.Timeout.Std())
	}
	if c.Proxy.RequestTimeout.Std() != 30*time.Second {
		t.Errorf("default RequestTimeout = %v, want 30s", c.Proxy.RequestTimeout.Std())
	}
}

func TestParse_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantSub string
	}{
		{
			name:    "empty listen",
			yaml:    "listen: \"\"\nstrategy: random\nbackends:\n  - url: http://a.com\n",
			wantSub: "listen address must not be empty",
		},
		{
			name:    "unknown strategy",
			yaml:    "listen: \":8080\"\nstrategy: magic\nbackends:\n  - url: http://a.com\n",
			wantSub: "unknown strategy",
		},
		{
			name:    "no backends",
			yaml:    "listen: \":8080\"\nstrategy: random\nbackends: []\n",
			wantSub: "at least one backend",
		},
		{
			name:    "bad url scheme",
			yaml:    "listen: \":8080\"\nstrategy: random\nbackends:\n  - url: ftp://a.com\n",
			wantSub: "scheme must be http or https",
		},
		{
			name:    "missing host",
			yaml:    "listen: \":8080\"\nstrategy: random\nbackends:\n  - url: http://\n",
			wantSub: "missing host",
		},
		{
			name:    "negative weight",
			yaml:    "listen: \":8080\"\nstrategy: random\nbackends:\n  - url: http://a.com\n    weight: -1\n",
			wantSub: "weight must be >= 1",
		},
		{
			// Explicit weight: 0 must be rejected, not silently promoted to 1.
			name:    "explicit zero weight",
			yaml:    "listen: \":8080\"\nstrategy: random\nbackends:\n  - url: http://a.com\n    weight: 0\n",
			wantSub: "weight must be >= 1",
		},
		{
			// Explicit interval: 0s must be rejected, not silently defaulted.
			name:    "explicit zero interval",
			yaml:    "listen: \":8080\"\nstrategy: random\nbackends:\n  - url: http://a.com\nhealth_check:\n  interval: 0s\n",
			wantSub: "interval must be > 0",
		},
		{
			// Explicit healthy_threshold: 0 must be rejected, not defaulted to 2.
			name:    "explicit zero healthy threshold",
			yaml:    "listen: \":8080\"\nstrategy: random\nbackends:\n  - url: http://a.com\nhealth_check:\n  healthy_threshold: 0\n",
			wantSub: "healthy_threshold must be >= 1",
		},
		{
			name:    "unknown field",
			yaml:    "listen: \":8080\"\nstrategy: random\nnope: true\nbackends:\n  - url: http://a.com\n",
			wantSub: "parse",
		},
		{
			name:    "bad duration",
			yaml:    "listen: \":8080\"\nstrategy: random\nbackends:\n  - url: http://a.com\nhealth_check:\n  interval: notaduration\n",
			wantSub: "invalid value",
		},
		{
			name:    "zero unhealthy threshold rejected when set explicitly negative",
			yaml:    "listen: \":8080\"\nstrategy: random\nbackends:\n  - url: http://a.com\nhealth_check:\n  unhealthy_threshold: -2\n",
			wantSub: "unhealthy_threshold must be >= 1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil {
				t.Fatalf("Parse() = nil error, want error containing %q", tt.wantSub)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("Parse() error = %q, want substring %q", err.Error(), tt.wantSub)
			}
		})
	}
}

func TestParse_ZeroRetriesAndInFlightAreValid(t *testing.T) {
	// Unlike thresholds, 0 is a legitimate value for these fields (no retries,
	// unlimited in-flight) and must not be rejected.
	const yaml = `
listen: ":8080"
strategy: random
backends:
  - url: http://a.com
proxy:
  max_retries: 0
  max_in_flight_per_backend: 0
`
	c, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}
	if c.Proxy.MaxRetries != 0 {
		t.Errorf("MaxRetries = %d, want 0", c.Proxy.MaxRetries)
	}
	if c.Proxy.MaxInFlightPerBackend != 0 {
		t.Errorf("MaxInFlightPerBackend = %d, want 0", c.Proxy.MaxInFlightPerBackend)
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("Load() = nil error, want error")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load() error = %v, want os.ErrNotExist", err)
	}
}

func TestLoad_RoundTripFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(validYAML), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if len(c.Backends) != 2 {
		t.Errorf("len(Backends) = %d, want 2", len(c.Backends))
	}
}

func TestExampleConfigIsValid(t *testing.T) {
	// The shipped example must always parse & validate.
	c, err := Load(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatalf("config.example.yaml is invalid: %v", err)
	}
	if c.Strategy == "" {
		t.Error("example config has empty strategy")
	}
}
