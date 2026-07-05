package main

import (
	"testing"

	"github.com/infobloxopen/devedge/internal/version"
)

// TestSameDaemonBuild covers the version-skew comparison `de start` uses (#56):
// the running daemon's reported build vs this client's build.
func TestSameDaemonBuild(t *testing.T) {
	cases := []struct {
		name         string
		ver, commit  string
		wantSameAsMe bool
	}{
		{"matches this build", version.Version, version.Commit, true},
		{"empty (old daemon) is skew", "", "", false},
		{"different version is skew", "v9.9.9", version.Commit, false},
		{"different commit is skew", version.Version, "deadbeef", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sameDaemonBuild(c.ver, c.commit); got != c.wantSameAsMe {
				t.Errorf("sameDaemonBuild(%q,%q) = %v, want %v", c.ver, c.commit, got, c.wantSameAsMe)
			}
		})
	}
}

// TestDaemonBuildLabel covers the human-readable build label, including the
// old-daemon (no version reported) case.
func TestDaemonBuildLabel(t *testing.T) {
	if got := daemonBuildLabel("", ""); got != "an older build (no version reported)" {
		t.Errorf("empty label = %q", got)
	}
	if got := daemonBuildLabel("v1.2.3", "abc123"); got != "devedge v1.2.3 (abc123)" {
		t.Errorf("label = %q", got)
	}
}
