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
type ipHash struct {
	// trustForwarded enables deriving the client IP from forwarding headers.
	// When false (default), only the TCP peer address is used, so clients cannot
	// forge X-Forwarded-For to steer their own routing.
	trustForwarded bool
}

func init() {
	register("ip-hash", func(o Options) Strategy {
		return ipHash{trustForwarded: o.TrustForwardedHeaders}
	})
}

func (ipHash) Name() string { return "ip-hash" }

func (h ipHash) Next(r *http.Request, candidates []*Backend) (*Backend, error) {
	n := len(candidates)
	if n == 0 {
		return nil, ErrNoHealthyBackends
	}
	idx := fnv1a32(clientIP(r, h.trustForwarded)) % uint32(n)
	return reserve(candidates[idx])
}

// clientIP determines the client's IP for sticky hashing.
//
// By default it uses the TCP peer address (RemoteAddr). When trustForwarded is
// set — i.e. Fulcrum is behind a proxy you trust to set forwarding headers — it
// prefers X-Forwarded-For (leftmost non-empty entry = original client) then
// X-Real-IP, and only then falls back to RemoteAddr. A blank or malformed
// forwarding value never collapses everything onto one backend; it falls through
// to the next source.
func clientIP(r *http.Request, trustForwarded bool) string {
	if r == nil {
		return ""
	}
	if trustForwarded {
		if ip := leftmostForwarded(r.Header.Get("X-Forwarded-For")); ip != "" {
			return ip
		}
		if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
			return xr
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// leftmostForwarded returns the first non-empty, trimmed entry of an
// X-Forwarded-For header value, or "" if there is none.
func leftmostForwarded(xff string) string {
	for _, part := range strings.Split(xff, ",") {
		if ip := strings.TrimSpace(part); ip != "" {
			return ip
		}
	}
	return ""
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
