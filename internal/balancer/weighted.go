package balancer

import (
	"net/http"
	"sync"
)

// weighted implements smooth weighted round-robin (the algorithm used by nginx).
// Over a full cycle it distributes requests exactly in proportion to weights,
// while interleaving picks so a high-weight backend is not selected in a long
// contiguous burst.
//
// It keeps a per-backend "current weight" that must persist across calls, so
// unlike round-robin it needs a lock. State is keyed by *Backend, so backends
// that drop out of the healthy set are simply never touched.
type weighted struct {
	mu      sync.Mutex
	current map[*Backend]int
}

func init() {
	register("weighted", func() Strategy {
		return &weighted{current: make(map[*Backend]int)}
	})
}

func (w *weighted) Name() string { return "weighted" }

func (w *weighted) Next(_ *http.Request, candidates []*Backend) (*Backend, error) {
	if len(candidates) == 0 {
		return nil, ErrNoHealthyBackends
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.pruneAbsent(candidates)

	var (
		best   *Backend
		bestCW int
		total  int
	)
	for _, b := range candidates {
		cw := w.current[b] + b.Weight
		w.current[b] = cw
		total += b.Weight
		if best == nil || cw > bestCW {
			best, bestCW = b, cw
		}
	}
	// The winner "pays" the total weight, so lower-weight peers catch up on
	// subsequent calls.
	w.current[best] -= total
	return reserve(best)
}

// pruneAbsent drops current-weight state for backends no longer among the
// candidates. Without this, a backend that leaves the healthy set (e.g. a health
// check marks it down) keeps its stale — possibly deeply negative — current
// weight and, on return, is either starved or burst-served, drifting the served
// ratio away from the configured weights. Pruning lets a returning backend
// restart fair at zero. The guard keeps the steady state (no churn) allocation-free.
func (w *weighted) pruneAbsent(candidates []*Backend) {
	if len(w.current) <= len(candidates) {
		return
	}
	present := make(map[*Backend]struct{}, len(candidates))
	for _, b := range candidates {
		present[b] = struct{}{}
	}
	for b := range w.current {
		if _, ok := present[b]; !ok {
			delete(w.current, b)
		}
	}
}
