package balancer

import "testing"

func mustBackend(t *testing.T, url string) *Backend {
	t.Helper()
	b, err := NewBackend(url, 1)
	if err != nil {
		t.Fatalf("NewBackend(%q): %v", url, err)
	}
	return b
}

func TestPool_HealthyFiltersUnhealthy(t *testing.T) {
	b1 := mustBackend(t, "http://a.com")
	b2 := mustBackend(t, "http://b.com")
	b3 := mustBackend(t, "http://c.com")
	p := NewPool([]*Backend{b1, b2, b3})

	if got := len(p.Healthy()); got != 3 {
		t.Fatalf("initial healthy = %d, want 3", got)
	}

	b2.SetHealthy(false)
	healthy := p.Healthy()
	if len(healthy) != 2 {
		t.Fatalf("healthy after down = %d, want 2", len(healthy))
	}
	for _, b := range healthy {
		if b == b2 {
			t.Error("unhealthy backend leaked into Healthy()")
		}
	}

	if p.Len() != 3 {
		t.Errorf("Len() = %d, want 3 (total unchanged)", p.Len())
	}
	if got := len(p.All()); got != 3 {
		t.Errorf("All() = %d, want 3", got)
	}
}

func TestPool_SnapshotIsIndependent(t *testing.T) {
	b1 := mustBackend(t, "http://a.com")
	src := []*Backend{b1}
	p := NewPool(src)

	// Mutating the caller's slice must not affect the pool.
	src[0] = nil
	if p.All()[0] != b1 {
		t.Error("pool shares the caller's backing array")
	}

	// Mutating a returned snapshot must not affect the pool.
	snap := p.All()
	snap[0] = nil
	if p.All()[0] != b1 {
		t.Error("returned snapshot shares the pool's backing array")
	}
}
