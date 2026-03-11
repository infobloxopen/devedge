// Package registry implements the route registry for devedge.
//
// The registry is the single source of truth for active routes. It supports
// lease-based registration, renewal, deregistration, and garbage collection
// of expired leases.
package registry

import (
	"fmt"
	"sync"
	"time"

	"github.com/infobloxopen/devedge/pkg/types"
)

// Event describes a mutation that occurred in the registry.
type Event struct {
	Kind  EventKind
	Route types.Route
	Time  time.Time
}

// EventKind identifies the type of registry mutation.
type EventKind string

const (
	EventRegistered   EventKind = "registered"
	EventRenewed      EventKind = "renewed"
	EventDeregistered EventKind = "deregistered"
	EventExpired      EventKind = "expired"
)

// Clock abstracts time for testing.
type Clock func() time.Time

// Registry holds the set of active routes keyed by host.
type Registry struct {
	mu       sync.RWMutex
	routes   map[string]types.Route
	clock    Clock
	onChange func(Event)
}

// Option configures a Registry.
type Option func(*Registry)

// WithClock sets the time source. Defaults to time.Now.
func WithClock(c Clock) Option {
	return func(r *Registry) { r.clock = c }
}

// SetClock updates the time source. Useful for testing time-dependent behavior.
func (r *Registry) SetClock(c Clock) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clock = c
}

// SetOnChange sets the mutation callback after construction.
func (r *Registry) SetOnChange(fn func(Event)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onChange = fn
}

// WithOnChange sets a callback invoked after every mutation.
func WithOnChange(fn func(Event)) Option {
	return func(r *Registry) { r.onChange = fn }
}

// New creates an empty registry.
func New(opts ...Option) *Registry {
	r := &Registry{
		routes: make(map[string]types.Route),
		clock:  time.Now,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Register adds or replaces a route. If a route with the same host already
// exists and is owned by a different owner, Register returns an error
// (first-writer-wins conflict policy).
func (r *Registry) Register(route types.Route) error {
	var evt Event

	r.mu.Lock()
	now := r.clock()

	if existing, ok := r.routes[route.Host]; ok {
		if existing.Owner != "" && route.Owner != "" && existing.Owner != route.Owner {
			r.mu.Unlock()
			return fmt.Errorf("conflict: host %q is owned by %q", route.Host, existing.Owner)
		}
	}

	kind := EventRegistered
	if _, exists := r.routes[route.Host]; exists {
		kind = EventRenewed
	}

	route.RenewedAt = now
	if kind == EventRegistered {
		route.CreatedAt = now
	} else {
		route.CreatedAt = r.routes[route.Host].CreatedAt
	}

	r.routes[route.Host] = route
	evt = Event{Kind: kind, Route: route, Time: now}
	r.mu.Unlock()

	r.emit(evt)
	return nil
}

// Deregister removes a route by host. Returns false if the host was not found.
func (r *Registry) Deregister(host string) bool {
	r.mu.Lock()

	route, ok := r.routes[host]
	if !ok {
		r.mu.Unlock()
		return false
	}
	delete(r.routes, host)
	evt := Event{Kind: EventDeregistered, Route: route, Time: r.clock()}
	r.mu.Unlock()

	r.emit(evt)
	return true
}

// DeregisterProject removes all routes belonging to the given project.
// Returns the number of routes removed.
func (r *Registry) DeregisterProject(project string) int {
	r.mu.Lock()

	now := r.clock()
	var events []Event
	for host, route := range r.routes {
		if route.Project == project {
			delete(r.routes, host)
			events = append(events, Event{Kind: EventDeregistered, Route: route, Time: now})
		}
	}
	r.mu.Unlock()

	for _, evt := range events {
		r.emit(evt)
	}
	return len(events)
}

// Lookup returns a route by host.
func (r *Registry) Lookup(host string) (types.Route, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	route, ok := r.routes[host]
	return route, ok
}

// List returns all active (non-expired) routes.
func (r *Registry) List() []types.Route {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := r.clock()
	out := make([]types.Route, 0, len(r.routes))
	for _, route := range r.routes {
		if !route.IsExpired(now) {
			out = append(out, route)
		}
	}
	return out
}

// Sweep removes expired leases and returns the number of routes cleaned up.
func (r *Registry) Sweep() int {
	r.mu.Lock()

	now := r.clock()
	var events []Event
	for host, route := range r.routes {
		if route.IsExpired(now) {
			delete(r.routes, host)
			events = append(events, Event{Kind: EventExpired, Route: route, Time: now})
		}
	}
	r.mu.Unlock()

	for _, evt := range events {
		r.emit(evt)
	}
	return len(events)
}

func (r *Registry) emit(e Event) {
	if r.onChange != nil {
		r.onChange(e)
	}
}
