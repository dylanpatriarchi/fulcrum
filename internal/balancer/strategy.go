package balancer

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
)

// ErrNoHealthyBackends is returned by a Strategy when no candidate is available.
var ErrNoHealthyBackends = errors.New("balancer: no healthy backends available")

// Strategy selects a backend for a request from a set of healthy candidates.
//
// Candidates are supplied by the caller (the Pool's healthy snapshot) rather
// than pulled from a shared pool, which keeps strategies decoupled and trivially
// testable. Implementations must be safe for concurrent use.
//
// Reservation contract: Next reserves an in-flight slot on the returned backend
// (via Backend.Acquire) as part of selection, so the active-connection count is
// consistent the instant the choice is visible. This is what makes
// least-connections correct under a simultaneous burst. The caller MUST call
// Backend.Release exactly once when the request completes.
type Strategy interface {
	// Name is the config identifier the strategy is registered under.
	Name() string
	// Next returns the chosen (and reserved) backend, or ErrNoHealthyBackends if
	// candidates is empty. r may be used by request-aware strategies (ip-hash).
	Next(r *http.Request, candidates []*Backend) (*Backend, error)
}

// reserve marks an in-flight slot on the chosen backend and returns it. Every
// strategy funnels its selection through this so the reservation contract is
// implemented in exactly one place.
func reserve(b *Backend) (*Backend, error) {
	b.Acquire()
	return b, nil
}

// Factory constructs a fresh Strategy instance (each has its own internal state,
// e.g. a round-robin cursor).
type Factory func() Strategy

// registry maps config names to strategy factories. It is populated by init()
// functions in this package and is read-only afterwards, so no locking is needed.
var registry = map[string]Factory{}

// register adds a strategy factory under name, panicking on a duplicate. It is
// intended to be called only from init().
func register(name string, f Factory) {
	if _, dup := registry[name]; dup {
		panic(fmt.Sprintf("balancer: strategy %q registered twice", name))
	}
	registry[name] = f
}

// New constructs the strategy registered under name.
func New(name string) (Strategy, error) {
	f, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("balancer: unknown strategy %q (available: %v)", name, Names())
	}
	return f(), nil
}

// Names returns the sorted list of registered strategy names.
func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
