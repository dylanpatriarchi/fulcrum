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

	// passiveFails counts consecutive request-time failures for passive health
	// checking; it is reset on the first success.
	passiveFails atomic.Int64

	// cumulative per-backend statistics.
	totalRequests atomic.Int64
	totalFailures atomic.Int64
}

// Stats is a point-in-time snapshot of a backend's counters.
type Stats struct {
	Healthy       bool  `json:"healthy"`
	ActiveConns   int64 `json:"active_conns"`
	TotalRequests int64 `json:"total_requests"`
	TotalFailures int64 `json:"total_failures"`
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

// setHealthy sets the backend's health state and reports whether it changed. It
// is unexported so all health changes flow through Pool.SetHealthy, which keeps
// the pool's cached healthy snapshot coherent; a direct setter would flip the
// flag without rebuilding the snapshot and silently fail to reroute traffic.
func (b *Backend) setHealthy(v bool) (changed bool) {
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

// RecordSuccess accounts a successful request and clears the passive-failure
// streak.
func (b *Backend) RecordSuccess() {
	b.totalRequests.Add(1)
	b.passiveFails.Store(0)
}

// RecordFailure accounts a failed request and returns the new consecutive
// passive-failure count.
func (b *Backend) RecordFailure() int64 {
	b.totalRequests.Add(1)
	b.totalFailures.Add(1)
	return b.passiveFails.Add(1)
}

// ResetPassiveFailures clears the passive-failure streak (e.g. when the backend
// is brought back into rotation).
func (b *Backend) ResetPassiveFailures() { b.passiveFails.Store(0) }

// Stats returns a snapshot of the backend's counters.
func (b *Backend) Stats() Stats {
	return Stats{
		Healthy:       b.Healthy(),
		ActiveConns:   b.active.Load(),
		TotalRequests: b.totalRequests.Load(),
		TotalFailures: b.totalFailures.Load(),
	}
}

// String returns the backend's URL, implementing fmt.Stringer for logs.
func (b *Backend) String() string { return b.URL.String() }
