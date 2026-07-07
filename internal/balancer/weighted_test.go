package balancer

import "testing"

func TestWeighted_EmptyCandidates(t *testing.T) {
	w := &weighted{current: map[*Backend]int{}}
	if _, err := w.Next(nil, nil); err != ErrNoHealthyBackends {
		t.Errorf("Next(empty) error = %v, want ErrNoHealthyBackends", err)
	}
}

// TestWeighted_DistributionMatchesWeights asserts smooth WRR is exactly
// proportional over a whole number of cycles (cycle length == sum of weights).
func TestWeighted_DistributionMatchesWeights(t *testing.T) {
	b1 := mustWeightedBackend(t, "http://a.com", 1)
	b2 := mustWeightedBackend(t, "http://b.com", 2)
	b3 := mustWeightedBackend(t, "http://c.com", 3)
	candidates := []*Backend{b1, b2, b3}

	w := &weighted{current: map[*Backend]int{}}

	const cycles = 1000
	total := (1 + 2 + 3) * cycles
	counts := map[*Backend]int{}
	for i := 0; i < total; i++ {
		b, err := w.Next(nil, candidates)
		if err != nil {
			t.Fatalf("Next() #%d: %v", i, err)
		}
		counts[b]++
	}

	want := map[*Backend]int{b1: 1 * cycles, b2: 2 * cycles, b3: 3 * cycles}
	for b, n := range want {
		if counts[b] != n {
			t.Errorf("backend weight %d served %d, want %d", b.Weight, counts[b], n)
		}
	}
}

// TestWeighted_Interleaves checks the smooth property: the highest-weight
// backend is not returned in one contiguous block.
func TestWeighted_Interleaves(t *testing.T) {
	b1 := mustWeightedBackend(t, "http://a.com", 1)
	b5 := mustWeightedBackend(t, "http://b.com", 5)
	candidates := []*Backend{b1, b5}

	w := &weighted{current: map[*Backend]int{}}

	// Over one cycle of 6 picks, b5 must not occupy the first 5 in a row (a naive
	// expand-by-weight scheme would). Assert b1 appears within the first 5 picks.
	seenLow := false
	for i := 0; i < 5; i++ {
		b, _ := w.Next(nil, candidates)
		if b == b1 {
			seenLow = true
		}
	}
	if !seenLow {
		t.Error("weight-1 backend not interleaved within the first 5 of 6 picks")
	}
}

func mustWeightedBackend(t *testing.T, url string, weight int) *Backend {
	t.Helper()
	b, err := NewBackend(url, weight)
	if err != nil {
		t.Fatalf("NewBackend(%q, %d): %v", url, weight, err)
	}
	return b
}
