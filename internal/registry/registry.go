// Package registry implements the route registry for devedge.
//
// The registry is the single source of truth for active routes. It supports
// lease-based registration, renewal, deregistration, and garbage collection
// of expired leases.
//
// A single host can hold multiple routes distinguished by URL path prefix
// (types.Route.Path). Routes are stored under a two-level map keyed by
// (host, path); Lookup selects the route whose Path is the longest prefix of
// the request path, with a path-less route ("") acting as the host's
// catch-all. A route with an empty Path preserves the original one-host,
// one-route behavior exactly.
package registry

import (
	"fmt"
	"strings"
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

// Registry holds the set of active routes keyed by (host, path). The outer map
// is keyed by host; the inner map is keyed by the route's Path (a path-less
// route uses the "" key).
type Registry struct {
	mu       sync.RWMutex
	routes   map[string]map[string]types.Route
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
		routes: make(map[string]map[string]types.Route),
		clock:  time.Now,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Register adds or replaces a route. Conflict and create/renew semantics are
// keyed by (host, Path): if a route with the same host AND path already exists
// and is owned by a different owner, Register returns an error (first-writer-
// wins). Registering a new path under an existing host is always additive.
func (r *Registry) Register(route types.Route) error {
	var evt Event

	r.mu.Lock()
	now := r.clock()

	byPath := r.routes[route.Host]
	existing, exists := byPath[route.Path]
	if exists {
		if existing.Owner != "" && route.Owner != "" && existing.Owner != route.Owner {
			r.mu.Unlock()
			return fmt.Errorf("conflict: host %q path %q is owned by %q", route.Host, route.Path, existing.Owner)
		}
	}

	kind := EventRegistered
	if exists {
		kind = EventRenewed
	}

	route.RenewedAt = now
	if kind == EventRegistered {
		route.CreatedAt = now
	} else {
		route.CreatedAt = existing.CreatedAt
	}

	if byPath == nil {
		byPath = make(map[string]types.Route)
		r.routes[route.Host] = byPath
	}
	byPath[route.Path] = route
	evt = Event{Kind: kind, Route: route, Time: now}
	r.mu.Unlock()

	r.emit(evt)
	return nil
}

// Deregister removes routes for a host. Called with an empty path it removes
// ALL routes registered under that host; with a non-empty path it removes only
// the single (host, path) route. Returns false if nothing matched.
func (r *Registry) Deregister(host string, path ...string) bool {
	r.mu.Lock()

	byPath, ok := r.routes[host]
	if !ok || len(byPath) == 0 {
		r.mu.Unlock()
		return false
	}

	now := r.clock()
	var events []Event

	if len(path) > 0 {
		// Remove just the one (host, path) route.
		route, found := byPath[path[0]]
		if !found {
			r.mu.Unlock()
			return false
		}
		delete(byPath, path[0])
		if len(byPath) == 0 {
			delete(r.routes, host)
		}
		events = append(events, Event{Kind: EventDeregistered, Route: route, Time: now})
	} else {
		// Remove every route for the host.
		for _, route := range byPath {
			events = append(events, Event{Kind: EventDeregistered, Route: route, Time: now})
		}
		delete(r.routes, host)
	}
	r.mu.Unlock()

	for _, evt := range events {
		r.emit(evt)
	}
	return len(events) > 0
}

// DeregisterProject removes all routes belonging to the given project.
// Returns the number of routes removed.
func (r *Registry) DeregisterProject(project string) int {
	r.mu.Lock()

	now := r.clock()
	var events []Event
	for host, byPath := range r.routes {
		for path, route := range byPath {
			if route.Project == project {
				delete(byPath, path)
				events = append(events, Event{Kind: EventDeregistered, Route: route, Time: now})
			}
		}
		if len(byPath) == 0 {
			delete(r.routes, host)
		}
	}
	r.mu.Unlock()

	for _, evt := range events {
		r.emit(evt)
	}
	return len(events)
}

// Lookup returns the route for host whose Path is the longest prefix of
// reqPath. A path-less route (Path == "") is the fallback catch-all and
// matches any request path. Returns false when the host has no routes at all
// or when no route's prefix matches reqPath.
func (r *Registry) Lookup(host string, reqPath ...string) (types.Route, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	byPath, ok := r.routes[host]
	if !ok || len(byPath) == 0 {
		return types.Route{}, false
	}

	// Default the request path to "/" so a single path-less route still
	// matches host-only lookups (e.g. TCP SNI dispatch, `de inspect HOST`).
	path := "/"
	if len(reqPath) > 0 {
		path = reqPath[0]
	}

	var best types.Route
	found := false
	bestLen := -1
	for p, route := range byPath {
		if !pathMatches(p, path) {
			continue
		}
		if len(p) > bestLen {
			best = route
			bestLen = len(p)
			found = true
		}
	}
	return best, found
}

// pathMatches reports whether the route prefix p matches request path reqPath.
// An empty prefix (catch-all) always matches. A non-empty prefix matches when
// reqPath equals it or is a sub-path of it, so "/api" matches "/api" and
// "/api/v1" but not "/apiary".
func pathMatches(p, reqPath string) bool {
	if p == "" {
		return true
	}
	if !strings.HasPrefix(reqPath, p) {
		return false
	}
	if len(reqPath) == len(p) {
		return true
	}
	// Boundary check: the char after the prefix must be a separator so "/api"
	// does not match "/apiary".
	return reqPath[len(p)] == '/' || strings.HasSuffix(p, "/")
}

// List returns all active (non-expired) routes across every host and path.
func (r *Registry) List() []types.Route {
	r.mu.RLock()
	defer r.mu.RUnlock()

	now := r.clock()
	out := make([]types.Route, 0, len(r.routes))
	for _, byPath := range r.routes {
		for _, route := range byPath {
			if !route.IsExpired(now) {
				out = append(out, route)
			}
		}
	}
	return out
}

// Sweep removes expired leases and returns the number of routes cleaned up.
func (r *Registry) Sweep() int {
	r.mu.Lock()

	now := r.clock()
	var events []Event
	for host, byPath := range r.routes {
		for path, route := range byPath {
			if route.IsExpired(now) {
				delete(byPath, path)
				events = append(events, Event{Kind: EventExpired, Route: route, Time: now})
			}
		}
		if len(byPath) == 0 {
			delete(r.routes, host)
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
