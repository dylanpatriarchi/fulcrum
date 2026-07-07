package balancer

import (
	"math"
	"net/http"
)

// leastConn selects the candidate with the fewest in-flight requests, reading
// each backend's atomic active-connection counter. It holds no state of its own
// and is race-free.
//
// Selection and the subsequent Acquire are not a single atomic step, so two
// concurrent picks may briefly choose the same backend; this is the accepted,
// self-correcting behaviour of least-connections balancing (the next pick sees
// the incremented count).
type leastConn struct{}

func init() {
	register("least-connections", func() Strategy { return leastConn{} })
}

func (leastConn) Name() string { return "least-connections" }

func (leastConn) Next(_ *http.Request, candidates []*Backend) (*Backend, error) {
	if len(candidates) == 0 {
		return nil, ErrNoHealthyBackends
	}
	var (
		best  *Backend
		least int64 = math.MaxInt64
	)
	for _, b := range candidates {
		if c := b.ActiveConns(); c < least {
			best, least = b, c
		}
	}
	return best, nil
}
