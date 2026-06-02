package cluster

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"regexp"
	"strings"

	"github.com/infobloxopen/devedge/internal/helm"
)

// Environment is the resolved operating mode (data-model.md). It is deterministic
// for a given process environment and is never inferred from cluster state.
type Environment string

const (
	// EnvDev is a developer machine — use the shared dev cluster (default).
	EnvDev Environment = "dev"
	// EnvEphemeral is CI / per-run — use a dedicated ephemeral cluster.
	EnvEphemeral Environment = "ephemeral"
)

// SharedClusterName is the single well-known shared dev cluster (D3/FR-002): one
// stable name per host so reuse is a trivial existence check.
const SharedClusterName = "devedge"

// DetectEnvironment resolves the operating mode with precedence (D2/FR-009):
// an explicit override (the --env/--ephemeral flag, else DEVEDGE_ENV) always
// wins; otherwise a truthy standard CI env var selects Ephemeral; otherwise Dev.
// flagOverride is "" when no flag was given.
func DetectEnvironment(flagOverride string) Environment {
	if env, ok := parseEnvOverride(flagOverride); ok {
		return env
	}
	if env, ok := parseEnvOverride(os.Getenv("DEVEDGE_ENV")); ok {
		return env
	}
	if isTruthy(os.Getenv("CI")) {
		return EnvEphemeral
	}
	return EnvDev
}

// parseEnvOverride maps an explicit override token to an Environment. "ci" is an
// alias for "ephemeral". Empty/unrecognized tokens are not an override (ok=false)
// so detection falls through to the next precedence level.
func parseEnvOverride(s string) (Environment, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "dev":
		return EnvDev, true
	case "ci", "ephemeral":
		return EnvEphemeral, true
	default:
		return "", false
	}
}

// isTruthy reports whether a boolean-ish env value is true. Empty, "0", "false",
// "no", "off" are false; any other non-empty value is true (CI providers
// conventionally set CI=true).
func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// ClusterTarget is the resolved destination for a project's routes + dependencies
// (data-model.md). Produced by Resolve, consumed by the CLI, threaded to the
// daemon's dependency provisioning.
type ClusterTarget struct {
	Name        string // k3d cluster name
	KubeContext string // provider context for Name (e.g. k3d-<Name>)
	Namespace   string // dependency namespace on that cluster (default devedge-deps)
	Ephemeral   bool   // created per-run and torn down by the wrapper
	Dedicated   bool   // resolved from the project's cluster.dedicated opt-in
}

// Resolve computes the cluster target for a project (D3/D4/D5):
//   - Ephemeral env → devedge-ci-<runid> (per-run unique), Ephemeral=true
//   - else dedicated opt-in → devedge-proj-<slug(project)>, Dedicated=true
//   - else → the shared "devedge" cluster
//
// KubeContext is derived through the Provider so resolution stays behind the
// adapter and never references k3d directly (Principle IV). Namespace defaults to
// the shared dependency namespace; per-(service,dependency) isolation within it is
// unchanged (003).
func Resolve(p Provider, env Environment, project string, dedicated bool) ClusterTarget {
	t := ClusterTarget{Namespace: helm.DefaultNamespace}
	switch {
	case env == EnvEphemeral:
		t.Name = "devedge-ci-" + RunID()
		t.Ephemeral = true
	case dedicated:
		t.Name = "devedge-proj-" + ProjectSlug(project)
		t.Dedicated = true
	default:
		t.Name = SharedClusterName
	}
	t.KubeContext = p.KubeContext(t.Name)
	return t
}

// RunID returns a per-run identifier for ephemeral cluster naming (D4): the first
// non-empty of GITHUB_RUN_ID / DEVEDGE_RUN_ID, else a random short token. The
// random fallback guarantees concurrent runs never collide and a crash leftover is
// uniquely named and discoverable.
func RunID() string {
	for _, k := range []string{"GITHUB_RUN_ID", "DEVEDGE_RUN_ID"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return slug(v)
		}
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ProjectSlug is the canonical, deterministic, DNS-safe name component devedge
// derives from a project/service name for any cluster resource it creates with a
// project-unique name — the dedicated cluster (`devedge-proj-<slug>`) and the
// rare dedicated per-service instance release (FR-016). Two distinct project names
// never collide on the same slug for ordinary inputs. 003's per-(service,
// dependency) store isolation uses its own binding slug and is unchanged.
func ProjectSlug(project string) string { return slug(project) }

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// slug normalizes an arbitrary string into a deterministic, DNS-safe cluster-name
// component: lowercased, runs of non-alphanumerics collapsed to single hyphens,
// edges trimmed. Empty input yields "default" so a name is always valid.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlnum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "default"
	}
	return s
}
