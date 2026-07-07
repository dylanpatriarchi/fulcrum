package balancer

import (
	"math/rand/v2"
	"net/http"
)

// random selects a uniformly random candidate. math/rand/v2's top-level
// functions are safe for concurrent use, so the strategy holds no state and is
// race-free.
type random struct{}

func init() {
	register("random", func() Strategy { return random{} })
}

func (random) Name() string { return "random" }

func (random) Next(_ *http.Request, candidates []*Backend) (*Backend, error) {
	n := len(candidates)
	if n == 0 {
		return nil, ErrNoHealthyBackends
	}
	return candidates[rand.IntN(n)], nil
}
