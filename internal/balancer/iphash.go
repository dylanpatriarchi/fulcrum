package balancer

import (
	"net"
	"net/http"
	"strings"
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
	idx := fnv1a32(clientIP(r)) % uint32(n)
	return reserve(candidates[idx])
}

// clientIP determines the client's IP for sticky hashing. When Fulcrum sits
// behind another proxy/LB/CDN, the TCP peer is that proxy, so a forwarding
// header carries the real client. We honour X-Forwarded-For (leftmost entry =
// original client) then X-Real-IP, falling back to the TCP peer address.
//
// This trusts the forwarding headers; deploy ip-hash behind a proxy you control
// (which should overwrite, not append, client-supplied values) to avoid clients
// steering their own stickiness.
func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		return xr
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// fnv1a32 computes the 32-bit FNV-1a hash of s without allocating (unlike
// fnv.New32a, which heap-allocates a hash.Hash32 behind an interface).
func fnv1a32(s string) uint32 {
	const (
		offset = 2166136261
		prime  = 16777619
	)
	h := uint32(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= prime
	}
	return h
}
