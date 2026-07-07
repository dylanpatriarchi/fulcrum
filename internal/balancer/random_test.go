package balancer

import "testing"

func TestRandom_EmptyCandidates(t *testing.T) {
	if _, err := (random{}).Next(nil, nil); err != ErrNoHealthyBackends {
		t.Errorf("Next(empty) error = %v, want ErrNoHealthyBackends", err)
	}
}

func TestRandom_StaysInRangeAndCoversAll(t *testing.T) {
	backends := []*Backend{
		mustBackend(t, "http://a.com"),
		mustBackend(t, "http://b.com"),
		mustBackend(t, "http://c.com"),
	}
	inSet := map[*Backend]bool{backends[0]: true, backends[1]: true, backends[2]: true}
	rnd := random{}

	counts := map[*Backend]int{}
	for i := 0; i < 3000; i++ {
		b, err := rnd.Next(nil, backends)
		if err != nil {
			t.Fatalf("Next() #%d: %v", i, err)
		}
		if !inSet[b] {
			t.Fatalf("Next() returned a backend not in the candidate set")
		}
		counts[b]++
	}
	// Every backend should be hit at least once over 3000 draws (probability of
	// missing one is astronomically small).
	for i, b := range backends {
		if counts[b] == 0 {
			t.Errorf("backend[%d] was never selected over 3000 draws", i)
		}
	}
}

func TestRandom_SingleCandidate(t *testing.T) {
	only := mustBackend(t, "http://a.com")
	for i := 0; i < 100; i++ {
		b, err := (random{}).Next(nil, []*Backend{only})
		if err != nil || b != only {
			t.Fatalf("Next() = %v, %v; want %s, nil", b, err, only)
		}
	}
}
