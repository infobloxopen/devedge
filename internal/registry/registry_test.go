package registry

import (
	"testing"
	"time"

	"github.com/infobloxopen/devedge/pkg/types"
)

func fixedClock(t time.Time) Clock {
	return func() time.Time { return t }
}

func TestRegister_and_Lookup(t *testing.T) {
	now := time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC)
	r := New(WithClock(fixedClock(now)))

	route := types.Route{
		Host:     "api.foo.dev.test",
		Upstream: "http://127.0.0.1:3000",
		Project:  "foo",
		Owner:    "cli",
		TTL:      30 * time.Second,
	}

	if err := r.Register(route); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, ok := r.Lookup("api.foo.dev.test")
	if !ok {
		t.Fatal("Lookup returned not found")
	}
	if got.Upstream != "http://127.0.0.1:3000" {
		t.Errorf("Upstream = %q, want %q", got.Upstream, "http://127.0.0.1:3000")
	}
	if got.CreatedAt != now {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, now)
	}
	if got.RenewedAt != now {
		t.Errorf("RenewedAt = %v, want %v", got.RenewedAt, now)
	}
}

func TestRegister_conflict(t *testing.T) {
	r := New()

	r.Register(types.Route{Host: "x.dev.test", Owner: "alice"})
	err := r.Register(types.Route{Host: "x.dev.test", Owner: "bob"})
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
}

func TestRegister_same_owner_renews(t *testing.T) {
	now := time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC)
	later := now.Add(10 * time.Second)

	clk := fixedClock(now)
	r := New(WithClock(clk))

	r.Register(types.Route{Host: "x.dev.test", Owner: "alice", Upstream: "http://a"})

	r.clock = fixedClock(later)
	err := r.Register(types.Route{Host: "x.dev.test", Owner: "alice", Upstream: "http://b"})
	if err != nil {
		t.Fatalf("same-owner re-register should succeed: %v", err)
	}

	got, _ := r.Lookup("x.dev.test")
	if got.Upstream != "http://b" {
		t.Errorf("Upstream not updated: %q", got.Upstream)
	}
	if got.CreatedAt != now {
		t.Errorf("CreatedAt should be preserved: got %v, want %v", got.CreatedAt, now)
	}
	if got.RenewedAt != later {
		t.Errorf("RenewedAt should be updated: got %v, want %v", got.RenewedAt, later)
	}
}

func TestDeregister(t *testing.T) {
	r := New()
	r.Register(types.Route{Host: "x.dev.test"})

	if !r.Deregister("x.dev.test") {
		t.Error("Deregister returned false for existing route")
	}
	if r.Deregister("x.dev.test") {
		t.Error("Deregister returned true for non-existing route")
	}
	if _, ok := r.Lookup("x.dev.test"); ok {
		t.Error("Lookup found deregistered route")
	}
}

func TestDeregisterProject(t *testing.T) {
	r := New()
	r.Register(types.Route{Host: "a.dev.test", Project: "foo"})
	r.Register(types.Route{Host: "b.dev.test", Project: "foo"})
	r.Register(types.Route{Host: "c.dev.test", Project: "bar"})

	n := r.DeregisterProject("foo")
	if n != 2 {
		t.Errorf("DeregisterProject removed %d, want 2", n)
	}

	if _, ok := r.Lookup("c.dev.test"); !ok {
		t.Error("bar route should not be removed")
	}
}

func TestList_excludes_expired(t *testing.T) {
	now := time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC)
	r := New(WithClock(fixedClock(now)))

	r.Register(types.Route{Host: "a.dev.test", TTL: 30 * time.Second})
	r.Register(types.Route{Host: "b.dev.test", TTL: 0}) // no expiry

	// Advance past TTL for route a
	r.clock = fixedClock(now.Add(60 * time.Second))

	list := r.List()
	if len(list) != 1 {
		t.Fatalf("List returned %d routes, want 1", len(list))
	}
	if list[0].Host != "b.dev.test" {
		t.Errorf("expected b.dev.test, got %q", list[0].Host)
	}
}

func TestSweep(t *testing.T) {
	now := time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC)
	r := New(WithClock(fixedClock(now)))

	r.Register(types.Route{Host: "a.dev.test", TTL: 10 * time.Second})
	r.Register(types.Route{Host: "b.dev.test", TTL: 0})

	r.clock = fixedClock(now.Add(30 * time.Second))
	swept := r.Sweep()
	if swept != 1 {
		t.Errorf("Sweep returned %d, want 1", swept)
	}

	if _, ok := r.Lookup("a.dev.test"); ok {
		t.Error("expired route should be removed by Sweep")
	}
	if _, ok := r.Lookup("b.dev.test"); !ok {
		t.Error("non-expiring route should survive Sweep")
	}
}

func TestLookup_longestPrefix(t *testing.T) {
	r := New()
	// Three routes on ONE host, distinguished by path prefix.
	if err := r.Register(types.Route{Host: "app.dev.test", Path: "", Upstream: "http://shell"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(types.Route{Host: "app.dev.test", Path: "/api", Upstream: "http://api"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(types.Route{Host: "app.dev.test", Path: "/api/v1", Upstream: "http://apiv1"}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		reqPath  string
		wantUp   string
		wantPath string
	}{
		{"/", "http://shell", ""},              // catch-all
		{"/index.html", "http://shell", ""},    // catch-all
		{"/api", "http://api", "/api"},         // exact
		{"/api/users", "http://api", "/api"},   // under /api
		{"/api/v1", "http://apiv1", "/api/v1"}, // longest wins
		{"/api/v1/x", "http://apiv1", "/api/v1"},
		{"/apiary", "http://shell", ""}, // boundary: not under /api
	}
	for _, c := range cases {
		got, ok := r.Lookup("app.dev.test", c.reqPath)
		if !ok {
			t.Errorf("Lookup(%q) not found", c.reqPath)
			continue
		}
		if got.Upstream != c.wantUp || got.Path != c.wantPath {
			t.Errorf("Lookup(%q) = upstream %q path %q, want upstream %q path %q",
				c.reqPath, got.Upstream, got.Path, c.wantUp, c.wantPath)
		}
	}
}

func TestLookup_noCatchAll_missesUnmatchedPath(t *testing.T) {
	r := New()
	// Only a path-scoped route, no catch-all.
	r.Register(types.Route{Host: "app.dev.test", Path: "/api", Upstream: "http://api"})

	if _, ok := r.Lookup("app.dev.test", "/other"); ok {
		t.Error("expected no match for /other when only /api is registered")
	}
	if got, ok := r.Lookup("app.dev.test", "/api/x"); !ok || got.Upstream != "http://api" {
		t.Errorf("expected /api/x to match /api route; ok=%v up=%q", ok, got.Upstream)
	}
	// Unknown host misses entirely.
	if _, ok := r.Lookup("nope.dev.test", "/"); ok {
		t.Error("expected miss for unknown host")
	}
}

func TestLookup_hostOnly_defaultsToRoot(t *testing.T) {
	r := New()
	r.Register(types.Route{Host: "x.dev.test", Upstream: "http://a"})
	// Host-only lookup (no reqPath) must still resolve the catch-all — this is
	// the path used by TCP SNI dispatch and `de inspect HOST`.
	got, ok := r.Lookup("x.dev.test")
	if !ok || got.Upstream != "http://a" {
		t.Errorf("host-only Lookup = %q ok=%v, want http://a true", got.Upstream, ok)
	}
}

func TestRegister_conflict_perHostPath(t *testing.T) {
	r := New()
	// Same host, DIFFERENT path, different owner: additive, no conflict.
	if err := r.Register(types.Route{Host: "app.dev.test", Path: "", Owner: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(types.Route{Host: "app.dev.test", Path: "/api", Owner: "bob"}); err != nil {
		t.Fatalf("different path should not conflict: %v", err)
	}
	// Same host, SAME path, different owner: conflict.
	if err := r.Register(types.Route{Host: "app.dev.test", Path: "/api", Owner: "carol"}); err == nil {
		t.Fatal("expected conflict for same (host, path) with a different owner")
	}
	// Same host, same path, same owner: renews.
	if err := r.Register(types.Route{Host: "app.dev.test", Path: "/api", Owner: "bob", Upstream: "http://new"}); err != nil {
		t.Fatalf("same-owner re-register should renew: %v", err)
	}
	got, _ := r.Lookup("app.dev.test", "/api")
	if got.Upstream != "http://new" {
		t.Errorf("renew did not update upstream: %q", got.Upstream)
	}
}

func TestDeregister_hostRemovesAllPaths(t *testing.T) {
	r := New()
	r.Register(types.Route{Host: "app.dev.test", Path: ""})
	r.Register(types.Route{Host: "app.dev.test", Path: "/api"})
	r.Register(types.Route{Host: "other.dev.test", Path: ""})

	if !r.Deregister("app.dev.test") {
		t.Fatal("Deregister(host) returned false")
	}
	if _, ok := r.Lookup("app.dev.test", "/"); ok {
		t.Error("catch-all should be gone after host deregister")
	}
	if _, ok := r.Lookup("app.dev.test", "/api"); ok {
		t.Error("/api should be gone after host deregister")
	}
	if _, ok := r.Lookup("other.dev.test", "/"); !ok {
		t.Error("other host should be untouched")
	}
}

func TestDeregister_singlePath(t *testing.T) {
	r := New()
	r.Register(types.Route{Host: "app.dev.test", Path: ""})
	r.Register(types.Route{Host: "app.dev.test", Path: "/api"})

	if !r.Deregister("app.dev.test", "/api") {
		t.Fatal("Deregister(host, path) returned false for existing route")
	}
	if _, ok := r.Lookup("app.dev.test", "/api"); !ok {
		t.Error("removing /api should leave the catch-all to serve /api")
	} else if got, _ := r.Lookup("app.dev.test", "/api"); got.Path != "" {
		t.Errorf("expected catch-all to serve /api after removing /api route, got path %q", got.Path)
	}
	// Removing a path that isn't registered returns false.
	if r.Deregister("app.dev.test", "/nope") {
		t.Error("Deregister of a non-existent path should return false")
	}
	// The catch-all remains.
	if _, ok := r.Lookup("app.dev.test", "/"); !ok {
		t.Error("catch-all should remain after single-path deregister")
	}
}

func TestDeregisterProject_multiPath(t *testing.T) {
	r := New()
	r.Register(types.Route{Host: "app.dev.test", Path: "", Project: "foo"})
	r.Register(types.Route{Host: "app.dev.test", Path: "/api", Project: "foo"})
	r.Register(types.Route{Host: "bar.dev.test", Path: "", Project: "bar"})

	n := r.DeregisterProject("foo")
	if n != 2 {
		t.Errorf("DeregisterProject removed %d, want 2", n)
	}
	if _, ok := r.Lookup("app.dev.test", "/"); ok {
		t.Error("all foo routes should be gone")
	}
	if _, ok := r.Lookup("bar.dev.test", "/"); !ok {
		t.Error("bar route should survive")
	}
}

func TestOnChange_events(t *testing.T) {
	var events []Event
	r := New(WithOnChange(func(e Event) {
		events = append(events, e)
	}))

	r.Register(types.Route{Host: "x.dev.test"})
	r.Register(types.Route{Host: "x.dev.test"})
	r.Deregister("x.dev.test")

	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	if events[0].Kind != EventRegistered {
		t.Errorf("event[0] = %s, want registered", events[0].Kind)
	}
	if events[1].Kind != EventRenewed {
		t.Errorf("event[1] = %s, want renewed", events[1].Kind)
	}
	if events[2].Kind != EventDeregistered {
		t.Errorf("event[2] = %s, want deregistered", events[2].Kind)
	}
}
