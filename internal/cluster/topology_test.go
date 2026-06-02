package cluster

import (
	"strings"
	"testing"

	"github.com/infobloxopen/devedge/internal/helm"
)

// CT-1: DetectEnvironment precedence — an explicit override (flag, else
// DEVEDGE_ENV) always wins; otherwise a truthy CI env var selects Ephemeral;
// otherwise Dev.
func TestDetectEnvironment(t *testing.T) {
	tests := []struct {
		name     string
		override string
		ci       string // CI env value ("" = falsey/unset)
		devEnv   string // DEVEDGE_ENV value ("" = unset)
		want     Environment
	}{
		{"no signals -> dev", "", "", "", EnvDev},
		{"truthy CI -> ephemeral", "", "true", "", EnvEphemeral},
		{"CI=1 truthy", "", "1", "", EnvEphemeral},
		{"CI=false -> dev", "", "false", "", EnvDev},
		{"CI=0 -> dev", "", "0", "", EnvDev},
		{"flag dev beats truthy CI", "dev", "true", "", EnvDev},
		{"flag ephemeral", "ephemeral", "", "", EnvEphemeral},
		{"flag ci alias -> ephemeral", "ci", "", "", EnvEphemeral},
		{"DEVEDGE_ENV beats CI", "", "true", "dev", EnvDev},
		{"flag beats DEVEDGE_ENV", "ephemeral", "", "dev", EnvEphemeral},
		{"unknown flag falls through to CI", "bogus", "true", "", EnvEphemeral},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CI", tc.ci)
			t.Setenv("DEVEDGE_ENV", tc.devEnv)
			if got := DetectEnvironment(tc.override); got != tc.want {
				t.Errorf("DetectEnvironment(%q) [CI=%q DEVEDGE_ENV=%q] = %q, want %q",
					tc.override, tc.ci, tc.devEnv, got, tc.want)
			}
		})
	}
}

// T014: ProjectSlug is deterministic, DNS-safe, and collision-free for distinct
// ordinary project names.
func TestProjectSlug(t *testing.T) {
	cases := map[string]string{
		"myapp":      "myapp",
		"My App!":    "my-app",
		"  Foo_Bar ": "foo-bar",
		"a/b.c":      "a-b-c",
		"":           "default",
	}
	for in, want := range cases {
		if got := ProjectSlug(in); got != want {
			t.Errorf("ProjectSlug(%q) = %q, want %q", in, got, want)
		}
	}
	if ProjectSlug("alpha") == ProjectSlug("beta") {
		t.Error("distinct projects must not collide")
	}
	// Stable across calls.
	if ProjectSlug("My App!") != ProjectSlug("My App!") {
		t.Error("ProjectSlug must be deterministic")
	}
}

// CT-2: Resolve produces the right ClusterTarget for dev / dedicated / ephemeral,
// with deterministic + collision-free names.
func TestResolve(t *testing.T) {
	p := &K3dProvider{}

	t.Run("dev -> shared cluster", func(t *testing.T) {
		got := Resolve(p, EnvDev, "myapp", false)
		want := ClusterTarget{
			Name:        "devedge",
			KubeContext: "k3d-devedge",
			Namespace:   helm.DefaultNamespace,
		}
		if got != want {
			t.Errorf("Resolve(dev) = %+v, want %+v", got, want)
		}
	})

	t.Run("dedicated opt-in -> own cluster", func(t *testing.T) {
		got := Resolve(p, EnvDev, "myapp", true)
		if got.Name != "devedge-proj-myapp" || !got.Dedicated || got.Ephemeral {
			t.Errorf("Resolve(dedicated) = %+v", got)
		}
		if got.KubeContext != "k3d-devedge-proj-myapp" {
			t.Errorf("KubeContext = %q", got.KubeContext)
		}
	})

	t.Run("ephemeral -> per-run cluster", func(t *testing.T) {
		t.Setenv("GITHUB_RUN_ID", "")
		t.Setenv("DEVEDGE_RUN_ID", "12345")
		got := Resolve(p, EnvEphemeral, "myapp", false)
		if got.Name != "devedge-ci-12345" || !got.Ephemeral {
			t.Errorf("Resolve(ephemeral) = %+v", got)
		}
		if got.KubeContext != "k3d-devedge-ci-12345" {
			t.Errorf("KubeContext = %q", got.KubeContext)
		}
	})

	t.Run("ephemeral wins over dedicated", func(t *testing.T) {
		t.Setenv("GITHUB_RUN_ID", "")
		t.Setenv("DEVEDGE_RUN_ID", "99")
		got := Resolve(p, EnvEphemeral, "myapp", true)
		if got.Name != "devedge-ci-99" || !got.Ephemeral {
			t.Errorf("expected ephemeral to win, got %+v", got)
		}
	})

	t.Run("distinct projects -> distinct dedicated names", func(t *testing.T) {
		a := Resolve(p, EnvDev, "alpha", true)
		b := Resolve(p, EnvDev, "beta", true)
		if a.Name == b.Name {
			t.Errorf("dedicated name collision: %q", a.Name)
		}
	})

	t.Run("project name slugged deterministically", func(t *testing.T) {
		got := Resolve(p, EnvDev, "My App!", true)
		if got.Name != "devedge-proj-my-app" {
			t.Errorf("slug = %q, want devedge-proj-my-app", got.Name)
		}
	})

	t.Run("ephemeral names per-run unique without run id", func(t *testing.T) {
		t.Setenv("GITHUB_RUN_ID", "")
		t.Setenv("DEVEDGE_RUN_ID", "")
		a := Resolve(p, EnvEphemeral, "x", false)
		b := Resolve(p, EnvEphemeral, "x", false)
		if a.Name == b.Name {
			t.Errorf("expected unique ephemeral names, both %q", a.Name)
		}
		if !strings.HasPrefix(a.Name, "devedge-ci-") {
			t.Errorf("bad ephemeral name %q", a.Name)
		}
	})
}
