package version

import (
	"fmt"
	"runtime/debug"
)

var (
	// Set via -ldflags at build time by goreleaser (and the Makefile). These
	// are the primary source of truth for a released binary.
	Version = "dev"
	Commit  = "unknown"
)

// String reports the running binary's build as "devedge <version> (<commit>)".
//
// The linker-injected Version/Commit are the primary source (a goreleaser
// build). When they are absent — i.e. the placeholder "dev"/"unknown" left by a
// plain `go install`/`go build`, a legitimate install path — String falls back
// to the build info the Go toolchain embeds: the module version for a
// `go install .../cmd/de@vX.Y.Z`, and the VCS revision for a source build. This
// keeps `de version` and `de --version` honest for both build paths.
func String() string {
	ver, commit := resolve()
	return fmt.Sprintf("devedge %s (%s)", ver, commit)
}

// resolve returns the effective version and commit, preferring the
// linker-injected values and falling back to runtime/debug build info.
func resolve() (ver, commit string) {
	ver, commit = Version, Commit
	if injected(ver) {
		return ver, commit
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ver, commit
	}
	// A `go install .../cmd/de@vX.Y.Z` records the tag as Main.Version; a source
	// build records "(devel)" (leave the placeholder in that case).
	if v := info.Main.Version; v != "" && v != "(devel)" {
		ver = v
	}
	// A source build within the VCS tree embeds the revision; a module-cache
	// install does not, so keep the "unknown" placeholder there.
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			commit = s.Value
			if len(commit) > 12 {
				commit = commit[:12]
			}
			break
		}
	}
	return ver, commit
}

// injected reports whether v is a real linker-injected version rather than the
// "dev"/"unknown"/empty placeholder that means "not set by a release build".
func injected(v string) bool {
	return v != "" && v != "dev" && v != "unknown"
}
