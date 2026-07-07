package balancer

import (
	"math"
	"net/http"
	"sync"
)

// leastConn selects the candidate with the fewest in-flight requests, reading
// each backend's atomic active-connection counter.
//
// Selection and the reservation (Acquire) are performed together under a mutex,
// so concurrent picks are serialised: each observes the previous pick's
// increment. This prevents a simultaneous burst on an idle pool from all
// selecting the same backend (which a lock-free read-then-acquire would allow).
// The scan is O(candidates) and cheap, so the lock is not a real bottleneck.
type leastConn struct {
	mu sync.Mutex
}

func init() {
	register("least-connections", func() Strategy { return &leastConn{} })
}

func (*leastConn) Name() string { return "least-connections" }

func (lc *leastConn) Next(_ *http.Request, candidates []*Backend) (*Backend, error) {
	if len(candidates) == 0 {
		return nil, ErrNoHealthyBackends
	}

	lc.mu.Lock()
	defer lc.mu.Unlock()

	var (
		best  *Backend
		least int64 = math.MaxInt64
	)
	for _, b := range candidates {
		if c := b.ActiveConns(); c < least {
			best, least = b, c
		}
	}
	return reserve(best)
}
