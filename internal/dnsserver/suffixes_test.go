package dnsserver

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewConfiguredSuffix_Canonicalizes(t *testing.T) {
	cases := []struct{ in, want string }{
		{"dev.test", "dev.test"},
		{"DEV.TEST", "dev.test"},
		{"Dev.Test.", "dev.test"},
		{"  example.com  ", "example.com"},
		{"a-b.c-d.test", "a-b.c-d.test"},
	}
	for _, c := range cases {
		got, err := NewConfiguredSuffix(c.in)
		if err != nil {
			t.Fatalf("NewConfiguredSuffix(%q) returned error: %v", c.in, err)
		}
		if got.Name != c.want {
			t.Errorf("NewConfiguredSuffix(%q) = %q, want %q", c.in, got.Name, c.want)
		}
	}
}

func TestNewConfiguredSuffix_Rejects(t *testing.T) {
	bad := []string{
		"",
		".",
		"-bad.test",
		"bad-.test",
		"bad..test",
		"under_score.test",
		"a.b.c.d/etc",
	}
	for _, in := range bad {
		if _, err := NewConfiguredSuffix(in); err == nil {
			t.Errorf("NewConfiguredSuffix(%q) succeeded, want error", in)
		}
	}
}

func mustSuffix(t *testing.T, s string) ConfiguredSuffix {
	t.Helper()
	cs, err := NewConfiguredSuffix(s)
	if err != nil {
		t.Fatalf("setup: NewConfiguredSuffix(%q): %v", s, err)
	}
	return cs
}

func TestAuthoritativeSet_ReplaceComputesDiff(t *testing.T) {
	s := NewAuthoritativeSet()

	added, removed := s.Replace([]ConfiguredSuffix{
		mustSuffix(t, "dev.test"),
		mustSuffix(t, "example.com"),
	})
	if len(added) != 2 || len(removed) != 0 {
		t.Fatalf("first Replace: added=%v removed=%v", added, removed)
	}

	added, removed = s.Replace([]ConfiguredSuffix{
		mustSuffix(t, "dev.test"),
		mustSuffix(t, "third.test"),
	})
	if len(added) != 1 || added[0].Name != "third.test" {
		t.Errorf("second Replace added=%v, want [third.test]", added)
	}
	if len(removed) != 1 || removed[0].Name != "example.com" {
		t.Errorf("second Replace removed=%v, want [example.com]", removed)
	}
}

func TestAuthoritativeSet_Match_LongestSuffix(t *testing.T) {
	s := NewAuthoritativeSet()
	s.Replace([]ConfiguredSuffix{
		mustSuffix(t, "dev.test"),
		mustSuffix(t, "foo.dev.test"),
	})

	cs, ok := s.Match("bar.foo.dev.test")
	if !ok {
		t.Fatal("expected match for bar.foo.dev.test")
	}
	if cs.Name != "foo.dev.test" {
		t.Errorf("longest-suffix match = %q, want %q", cs.Name, "foo.dev.test")
	}

	cs, ok = s.Match("baz.dev.test")
	if !ok || cs.Name != "dev.test" {
		t.Errorf("Match(baz.dev.test) = %q,%v want dev.test,true", cs.Name, ok)
	}
}

func TestAuthoritativeSet_Match_TrailingDotAndCase(t *testing.T) {
	s := NewAuthoritativeSet()
	s.Replace([]ConfiguredSuffix{mustSuffix(t, "dev.test")})

	cs, ok := s.Match("Foo.DEV.test.")
	if !ok || cs.Name != "dev.test" {
		t.Errorf("Match(Foo.DEV.test.) = %q,%v want dev.test,true", cs.Name, ok)
	}

	cs, ok = s.Match("dev.test.")
	if !ok || cs.Name != "dev.test" {
		t.Errorf("Match(dev.test.) = %q,%v want dev.test,true", cs.Name, ok)
	}
}

func TestAuthoritativeSet_Match_OutOfSuffix(t *testing.T) {
	s := NewAuthoritativeSet()
	s.Replace([]ConfiguredSuffix{mustSuffix(t, "dev.test")})

	if _, ok := s.Match("example.com"); ok {
		t.Errorf("expected example.com to be unmatched")
	}
	// A name that is a label-suffix string but not a DNS suffix.
	if _, ok := s.Match("notdev.test"); ok {
		t.Errorf("expected notdev.test to be unmatched (not a subdomain of dev.test)")
	}
}

func TestAuthoritativeSet_Snapshot_SortedCopy(t *testing.T) {
	s := NewAuthoritativeSet()
	s.Replace([]ConfiguredSuffix{
		mustSuffix(t, "b.test"),
		mustSuffix(t, "a.test"),
	})
	snap := s.Snapshot()
	if len(snap) != 2 || snap[0].Name != "a.test" || snap[1].Name != "b.test" {
		t.Errorf("Snapshot = %v, want [a.test b.test]", snap)
	}
	// Mutating returned slice must not affect the set.
	snap[0] = ConfiguredSuffix{Name: "zzz"}
	again := s.Snapshot()
	if again[0].Name != "a.test" {
		t.Errorf("Snapshot mutation leaked into the set: %v", again)
	}
}

func TestAuthoritativeSet_ConcurrentReplaceAndMatch(t *testing.T) {
	s := NewAuthoritativeSet()
	s.Replace([]ConfiguredSuffix{mustSuffix(t, "dev.test")})

	stop := make(chan struct{})
	var matches atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if _, ok := s.Match("foo.dev.test"); ok {
						matches.Add(1)
					}
				}
			}
		}()
	}

	for i := 0; i < 100; i++ {
		s.Replace([]ConfiguredSuffix{
			mustSuffix(t, "dev.test"),
			mustSuffix(t, "example.com"),
		})
		s.Replace([]ConfiguredSuffix{mustSuffix(t, "dev.test")})
	}

	// dev.test is present in every snapshot, so any scheduled reader must observe
	// a match. On a constrained runner the main goroutine can finish all Replaces
	// before a reader is ever scheduled; wait (bounded) for readers to run so the
	// assertion tests concurrency, not the scheduler's luck.
	deadline := time.Now().Add(5 * time.Second)
	for matches.Load() == 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}

	close(stop)
	wg.Wait()

	if matches.Load() == 0 {
		t.Error("expected concurrent readers to observe matches")
	}
}
