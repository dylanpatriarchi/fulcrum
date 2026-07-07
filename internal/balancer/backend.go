// Package balancer contains the backend pool and the pluggable load-balancing
// strategies that select a backend for each incoming request.
package balancer

import (
	"fmt"
	"net/url"
	"sync/atomic"
)

// Backend is a single upstream target and its concurrency-safe runtime state.
//
// The zero value is not usable; construct with NewBackend. All mutable state is
// accessed through atomics so a Backend can be read and updated from the proxy,
// the health checker and the strategies concurrently without locking.
type Backend struct {
	URL    *url.URL
	Weight int

	healthy atomic.Bool  // current health state; toggled by the health checker
	active  atomic.Int64 // in-flight requests dispatched to this backend
}

// NewBackend parses rawURL and returns a Backend that starts in the healthy
// state (active health checking may later mark it down). weight must be >= 1.
func NewBackend(rawURL string, weight int) (*Backend, error) {
	if weight < 1 {
		return nil, fmt.Errorf("backend %q: weight must be >= 1, got %d", rawURL, weight)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("backend %q: %w", rawURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("backend %q: scheme must be http or https", rawURL)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("backend %q: missing host", rawURL)
	}
	b := &Backend{URL: u, Weight: weight}
	b.healthy.Store(true)
	return b, nil
}

// Healthy reports whether the backend is currently eligible to serve traffic.
func (b *Backend) Healthy() bool { return b.healthy.Load() }

// SetHealthy sets the backend's health state and reports whether it changed.
func (b *Backend) SetHealthy(v bool) (changed bool) {
	return b.healthy.Swap(v) != v
}

// ActiveConns returns the number of in-flight requests on this backend.
func (b *Backend) ActiveConns() int64 { return b.active.Load() }

// Acquire records the start of a request and returns the new in-flight count.
func (b *Backend) Acquire() int64 { return b.active.Add(1) }

// Release records the completion of a request. It never drops below zero.
func (b *Backend) Release() {
	if b.active.Add(-1) < 0 {
		// Defensive: a Release without a matching Acquire is a bug; clamp so the
		// least-connections strategy can never see a negative count.
		b.active.Store(0)
	}
}

// String returns the backend's URL, implementing fmt.Stringer for logs.
func (b *Backend) String() string { return b.URL.String() }
