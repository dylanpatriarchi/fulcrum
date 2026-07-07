package balancer

import "sync"

// Pool owns the set of backends and exposes the currently healthy subset to the
// balancing strategies. It is safe for concurrent use.
type Pool struct {
	mu       sync.RWMutex
	backends []*Backend
}

// NewPool returns a Pool over the given backends. The slice is copied so later
// mutation of the caller's slice does not affect the pool.
func NewPool(backends []*Backend) *Pool {
	cp := make([]*Backend, len(backends))
	copy(cp, backends)
	return &Pool{backends: cp}
}

// All returns a snapshot of every backend, regardless of health.
func (p *Pool) All() []*Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*Backend, len(p.backends))
	copy(out, p.backends)
	return out
}

// Healthy returns a snapshot of the backends currently marked healthy. The
// returned slice is freshly allocated and owned by the caller; the *Backend
// pointers are shared (their state is atomic).
func (p *Pool) Healthy() []*Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*Backend, 0, len(p.backends))
	for _, b := range p.backends {
		if b.Healthy() {
			out = append(out, b)
		}
	}
	return out
}

// Len returns the total number of backends in the pool.
func (p *Pool) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.backends)
}
