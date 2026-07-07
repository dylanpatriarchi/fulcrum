package balancer

import (
	"sync"
	"testing"
)

func TestRoundRobin_EmptyCandidates(t *testing.T) {
	rr := &roundRobin{}
	if _, err := rr.Next(nil, nil); err != ErrNoHealthyBackends {
		t.Errorf("Next(empty) error = %v, want ErrNoHealthyBackends", err)
	}
}

func TestRoundRobin_CyclesInOrder(t *testing.T) {
	backends := []*Backend{
		mustBackend(t, "http://a.com"),
		mustBackend(t, "http://b.com"),
		mustBackend(t, "http://c.com"),
	}
	rr := &roundRobin{}

	// Two full cycles must visit backends in strict order 0,1,2,0,1,2.
	want := []*Backend{backends[0], backends[1], backends[2], backends[0], backends[1], backends[2]}
	for i, w := range want {
		got, err := rr.Next(nil, backends)
		if err != nil {
			t.Fatalf("Next() #%d error: %v", i, err)
		}
		if got != w {
			t.Errorf("Next() #%d = %s, want %s", i, got, w)
		}
	}
}

// TestRoundRobin_Distribution asserts an even spread across backends.
func TestRoundRobin_Distribution(t *testing.T) {
	const (
		nBackends  = 4
		perBackend = 1000
		nRequests  = nBackends * perBackend
	)
	backends := make([]*Backend, nBackends)
	for i := range backends {
		backends[i] = mustBackend(t, "http://backend")
	}
	rr := &roundRobin{}

	counts := make(map[*Backend]int, nBackends)
	for i := 0; i < nRequests; i++ {
		b, err := rr.Next(nil, backends)
		if err != nil {
			t.Fatalf("Next() #%d: %v", i, err)
		}
		counts[b]++
	}

	// With a perfectly divisible request count, distribution is exact.
	for i, b := range backends {
		if counts[b] != perBackend {
			t.Errorf("backend[%d] served %d requests, want %d", i, counts[b], perBackend)
		}
	}
}

// TestRoundRobin_ConcurrentDistribution hammers Next from many goroutines and
// asserts the total spread stays within one request of even. Run with -race.
func TestRoundRobin_ConcurrentDistribution(t *testing.T) {
	const (
		nBackends   = 8
		nGoroutines = 16
		perG        = 500
		total       = nGoroutines * perG
	)
	backends := make([]*Backend, nBackends)
	for i := range backends {
		backends[i] = mustBackend(t, "http://backend")
	}
	rr := &roundRobin{}

	var mu sync.Mutex
	counts := make(map[*Backend]int, nBackends)

	var wg sync.WaitGroup
	wg.Add(nGoroutines)
	for g := 0; g < nGoroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				b, err := rr.Next(nil, backends)
				if err != nil {
					t.Errorf("Next(): %v", err)
					return
				}
				mu.Lock()
				counts[b]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Every request accounted for.
	sum := 0
	for _, c := range counts {
		sum += c
	}
	if sum != total {
		t.Fatalf("total served = %d, want %d", sum, total)
	}
	// The atomic cursor guarantees a near-perfect spread: exactly total/nBackends
	// since total is divisible by nBackends.
	want := total / nBackends
	for i, b := range backends {
		if counts[b] != want {
			t.Errorf("backend[%d] served %d, want %d", i, counts[b], want)
		}
	}
}
