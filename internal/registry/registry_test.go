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
