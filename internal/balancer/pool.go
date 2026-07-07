package balancer

import (
	"sync"
	"sync/atomic"
)

// Pool owns the set of backends and exposes the currently healthy subset to the
// balancing strategies. The backend set is fixed at construction; only health
// state changes over time.
//
// The healthy subset is cached as an atomically-swapped snapshot so the hot read
// path (Healthy, called per request) is allocation- and lock-free. The snapshot
// is rebuilt only when a backend's health actually changes.
type Pool struct {
	backends []*Backend // immutable after construction

	mu      sync.Mutex                 // serialises snapshot rebuilds
	healthy atomic.Pointer[[]*Backend] // cached healthy snapshot
}

// NewPool returns a Pool over the given backends. The slice is copied so later
// mutation of the caller's slice does not affect the pool.
func NewPool(backends []*Backend) *Pool {
	cp := make([]*Backend, len(backends))
	copy(cp, backends)
	p := &Pool{backends: cp}
	p.rebuild()
	return p
}

// rebuild recomputes the cached healthy snapshot. Callers must hold p.mu (or be
// the constructor, before the pool is shared).
func (p *Pool) rebuild() {
	out := make([]*Backend, 0, len(p.backends))
	for _, b := range p.backends {
		if b.Healthy() {
			out = append(out, b)
		}
	}
	p.healthy.Store(&out)
}

// Healthy returns the cached snapshot of currently healthy backends.
//
// The returned slice is shared and MUST NOT be modified by the caller; treat it
// as read-only. It is safe to read concurrently.
func (p *Pool) Healthy() []*Backend {
	return *p.healthy.Load()
}

// SetHealthy updates a backend's health state and, if it changed, rebuilds the
// cached healthy snapshot. It returns whether the state changed. This is the
// canonical way to toggle backend health so the snapshot stays coherent.
func (p *Pool) SetHealthy(b *Backend, v bool) (changed bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !b.SetHealthy(v) {
		return false
	}
	p.rebuild()
	return true
}

// All returns a snapshot of every backend, regardless of health.
func (p *Pool) All() []*Backend {
	out := make([]*Backend, len(p.backends))
	copy(out, p.backends)
	return out
}

// Len returns the total number of backends in the pool.
func (p *Pool) Len() int { return len(p.backends) }
