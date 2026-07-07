package balancer

import (
	"net/http"
	"sync/atomic"
)

// roundRobin distributes requests evenly across candidates using a monotonic
// atomic cursor, so it is race-free without locking.
type roundRobin struct {
	cursor atomic.Uint64
}

func init() {
	register("round-robin", func(Options) Strategy { return &roundRobin{} })
}

func (rr *roundRobin) Name() string { return "round-robin" }

func (rr *roundRobin) Next(_ *http.Request, candidates []*Backend) (*Backend, error) {
	n := len(candidates)
	if n == 0 {
		return nil, ErrNoHealthyBackends
	}
	// Add returns the post-increment value; subtract 1 so the first pick is
	// index 0. Unsigned modulo keeps the index in range as the cursor wraps.
	i := (rr.cursor.Add(1) - 1) % uint64(n)
	return reserve(candidates[i])
}
