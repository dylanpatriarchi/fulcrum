package balancer

import (
	"sync"
	"testing"
)

func TestLeastConn_EmptyCandidates(t *testing.T) {
	if _, err := (leastConn{}).Next(nil, nil); err != ErrNoHealthyBackends {
		t.Errorf("Next(empty) error = %v, want ErrNoHealthyBackends", err)
	}
}

func TestLeastConn_PicksFewestConnections(t *testing.T) {
	b1 := mustBackend(t, "http://a.com")
	b2 := mustBackend(t, "http://b.com")
	b3 := mustBackend(t, "http://c.com")
	candidates := []*Backend{b1, b2, b3}

	// Load b1 and b3; b2 has the fewest and must be chosen.
	b1.Acquire()
	b1.Acquire()
	b3.Acquire()

	got, err := (leastConn{}).Next(nil, candidates)
	if err != nil {
		t.Fatalf("Next(): %v", err)
	}
	if got != b2 {
		t.Errorf("Next() = %s (active %d), want b2 (active %d)", got, got.ActiveConns(), b2.ActiveConns())
	}
}

// TestLeastConn_ConcurrentConsistency hammers select+acquire+release from many
// goroutines. Run with -race: it asserts the atomic counters never corrupt and
// that every acquired connection is released (all counters return to zero).
func TestLeastConn_ConcurrentConsistency(t *testing.T) {
	const (
		nBackends   = 6
		nGoroutines = 24
		perG        = 2000
	)
	backends := make([]*Backend, nBackends)
	for i := range backends {
		backends[i] = mustBackend(t, "http://backend")
	}
	lc := leastConn{}

	var wg sync.WaitGroup
	wg.Add(nGoroutines)
	for g := 0; g < nGoroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				b, err := lc.Next(nil, backends)
				if err != nil {
					t.Errorf("Next(): %v", err)
					return
				}
				b.Acquire()
				// Simulate a unit of work by immediately releasing; the point is
				// to exercise the counters under contention, not to hold load.
				b.Release()
			}
		}()
	}
	wg.Wait()

	for i, b := range backends {
		if got := b.ActiveConns(); got != 0 {
			t.Errorf("backend[%d] ActiveConns = %d, want 0 after drain", i, got)
		}
	}
}

// TestLeastConn_BalancesUnderHold verifies that when connections are held open,
// least-connections spreads new work rather than piling onto one backend.
func TestLeastConn_BalancesUnderHold(t *testing.T) {
	b1 := mustBackend(t, "http://a.com")
	b2 := mustBackend(t, "http://b.com")
	candidates := []*Backend{b1, b2}
	lc := leastConn{}

	// Each iteration picks the least-loaded backend and holds the connection.
	for i := 0; i < 10; i++ {
		b, err := lc.Next(nil, candidates)
		if err != nil {
			t.Fatalf("Next(): %v", err)
		}
		b.Acquire()
	}

	// With held connections, the two backends must be within one of each other.
	diff := b1.ActiveConns() - b2.ActiveConns()
	if diff < 0 {
		diff = -diff
	}
	if diff > 1 {
		t.Errorf("imbalance: b1=%d b2=%d (diff %d > 1)", b1.ActiveConns(), b2.ActiveConns(), diff)
	}
}
