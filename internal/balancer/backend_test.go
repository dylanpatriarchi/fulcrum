package balancer

import (
	"strings"
	"testing"
)

func TestNewBackend(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		weight  int
		wantErr string
	}{
		{name: "valid http", url: "http://127.0.0.1:9001", weight: 1},
		{name: "valid https", url: "https://example.com", weight: 5},
		{name: "zero weight", url: "http://a.com", weight: 0, wantErr: "weight must be >= 1"},
		{name: "negative weight", url: "http://a.com", weight: -3, wantErr: "weight must be >= 1"},
		{name: "bad scheme", url: "ftp://a.com", weight: 1, wantErr: "scheme must be http or https"},
		{name: "missing host", url: "http://", weight: 1, wantErr: "missing host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := NewBackend(tt.url, tt.weight)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NewBackend() error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewBackend() unexpected error: %v", err)
			}
			if !b.Healthy() {
				t.Error("new backend should start healthy")
			}
			if b.Weight != tt.weight {
				t.Errorf("Weight = %d, want %d", b.Weight, tt.weight)
			}
		})
	}
}

func TestBackend_SetHealthy(t *testing.T) {
	b, _ := NewBackend("http://a.com", 1)

	if changed := b.SetHealthy(true); changed {
		t.Error("SetHealthy(true) on already-healthy backend reported changed")
	}
	if changed := b.SetHealthy(false); !changed {
		t.Error("SetHealthy(false) should report changed")
	}
	if b.Healthy() {
		t.Error("backend should be unhealthy after SetHealthy(false)")
	}
	if changed := b.SetHealthy(false); changed {
		t.Error("SetHealthy(false) on already-unhealthy backend reported changed")
	}
}

func TestBackend_AcquireRelease(t *testing.T) {
	b, _ := NewBackend("http://a.com", 1)

	if got := b.Acquire(); got != 1 {
		t.Errorf("Acquire() = %d, want 1", got)
	}
	if got := b.Acquire(); got != 2 {
		t.Errorf("Acquire() = %d, want 2", got)
	}
	if got := b.ActiveConns(); got != 2 {
		t.Errorf("ActiveConns() = %d, want 2", got)
	}
	b.Release()
	if got := b.ActiveConns(); got != 1 {
		t.Errorf("ActiveConns() after Release = %d, want 1", got)
	}
}

func TestBackend_ReleaseClampsAtZero(t *testing.T) {
	b, _ := NewBackend("http://a.com", 1)
	b.Release() // unmatched release
	if got := b.ActiveConns(); got != 0 {
		t.Errorf("ActiveConns() = %d, want 0 (clamped)", got)
	}
}
