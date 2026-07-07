package balancer

import (
	"hash/fnv"
	"net"
	"net/http"
)

// ipHash provides sticky sessions: a given client IP is consistently routed to
// the same backend, as long as the healthy candidate set is stable. It is
// stateless and race-free.
//
// Stickiness is relative to the candidate slice supplied by the caller: when a
// backend joins or leaves the healthy set, the modulo mapping shifts for some
// clients. That is the inherent trade-off of hash-based stickiness without a
// consistent-hash ring.
type ipHash struct{}

func init() {
	register("ip-hash", func() Strategy { return ipHash{} })
}

func (ipHash) Name() string { return "ip-hash" }

func (ipHash) Next(r *http.Request, candidates []*Backend) (*Backend, error) {
	n := len(candidates)
	if n == 0 {
		return nil, ErrNoHealthyBackends
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(clientIP(r)))
	return candidates[h.Sum32()%uint32(n)], nil
}

// clientIP extracts the client's IP from the request, stripping the port. It
// falls back to the raw RemoteAddr if it cannot be split.
func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
