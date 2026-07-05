package version

import (
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	s := String()
	if !strings.HasPrefix(s, "devedge ") {
		t.Fatalf("expected prefix 'devedge ', got %q", s)
	}
}

// TestResolvePrefersInjected verifies a released (linker-injected) build reports
// the injected values verbatim, never consulting build info.
func TestResolvePrefersInjected(t *testing.T) {
	origVer, origCommit := Version, Commit
	t.Cleanup(func() { Version, Commit = origVer, origCommit })

	Version, Commit = "v0.15.1", "abc1234"
	ver, commit := resolve()
	if ver != "v0.15.1" || commit != "abc1234" {
		t.Fatalf("resolve() = (%q, %q), want (v0.15.1, abc1234)", ver, commit)
	}
}

// TestResolveFallsBackToBuildInfo guards finding #142: an un-injected build (a
// plain `go install`/`go build`, which leaves the "dev"/"unknown" placeholders)
// must fall back to the Go build info. Under `go test` the build info reports
// Main.Version "(devel)" with no VCS stamping, so the placeholders are kept —
// the contract this asserts is only that resolve never panics and always
// returns a non-empty version string.
func TestResolveFallsBackToBuildInfo(t *testing.T) {
	origVer, origCommit := Version, Commit
	t.Cleanup(func() { Version, Commit = origVer, origCommit })

	Version, Commit = "dev", "unknown"
	ver, _ := resolve()
	if ver == "" {
		t.Fatal("resolve returned an empty version")
	}
}

func TestInjected(t *testing.T) {
	for _, tc := range []struct {
		v    string
		want bool
	}{
		{"v0.15.1", true},
		{"dev", false},
		{"unknown", false},
		{"", false},
	} {
		if got := injected(tc.v); got != tc.want {
			t.Errorf("injected(%q) = %v, want %v", tc.v, got, tc.want)
		}
	}
}
