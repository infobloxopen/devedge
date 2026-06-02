package main

import (
	"context"
	"errors"
	"testing"

	"github.com/infobloxopen/devedge/internal/cluster"
)

// fakeEnsurer records ensure/teardown calls so runCI's orchestration is tested
// without a real cluster.
type fakeEnsurer struct {
	ensureErr error
	created   string
	tornDown  []string
}

func (f *fakeEnsurer) EnsureEphemeral(context.Context) (cluster.ClusterTarget, error) {
	if f.ensureErr != nil {
		return cluster.ClusterTarget{}, f.ensureErr
	}
	f.created = "devedge-ci-test"
	return cluster.ClusterTarget{Name: f.created, KubeContext: "k3d-" + f.created, Ephemeral: true}, nil
}

func (f *fakeEnsurer) Teardown(name string) error {
	f.tornDown = append(f.tornDown, name)
	return nil
}

// FR-007 + FR-009: the ephemeral cluster is torn down on every exit path and the
// wrapped command's exit code is propagated (not turned into an error).
func TestRunCI_teardownAndExitCode(t *testing.T) {
	t.Run("success tears down, code 0", func(t *testing.T) {
		f := &fakeEnsurer{}
		code, err := runCI(context.Background(), f, []string{"true"},
			func(context.Context, cluster.ClusterTarget, []string) (int, error) { return 0, nil })
		if err != nil || code != 0 {
			t.Fatalf("got code=%d err=%v, want 0/nil", code, err)
		}
		if len(f.tornDown) != 1 || f.tornDown[0] != "devedge-ci-test" {
			t.Errorf("teardown = %v, want [devedge-ci-test]", f.tornDown)
		}
	})

	t.Run("wrapped failure still tears down, propagates code", func(t *testing.T) {
		f := &fakeEnsurer{}
		code, err := runCI(context.Background(), f, []string{"false"},
			func(context.Context, cluster.ClusterTarget, []string) (int, error) { return 3, nil })
		if err != nil {
			t.Fatalf("wrapped failure must not surface as an error: %v", err)
		}
		if code != 3 {
			t.Errorf("exit code = %d, want 3 (propagated)", code)
		}
		if len(f.tornDown) != 1 {
			t.Errorf("teardown must run on failure, got %v", f.tornDown)
		}
	})

	t.Run("ensure failure: error, nothing to tear down", func(t *testing.T) {
		f := &fakeEnsurer{ensureErr: errors.New("k3d down")}
		_, err := runCI(context.Background(), f, []string{"x"},
			func(context.Context, cluster.ClusterTarget, []string) (int, error) {
				t.Fatal("runner must not be called when ensure fails")
				return 0, nil
			})
		if err == nil {
			t.Fatal("expected an error when ensure fails")
		}
		if len(f.tornDown) != 0 {
			t.Errorf("nothing was created, so nothing should be torn down, got %v", f.tornDown)
		}
	})
}
