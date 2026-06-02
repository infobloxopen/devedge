package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/cluster"
)

// ciCmd groups CI-oriented helpers.
func ciCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ci",
		Short: "CI helpers for ephemeral, per-run clusters",
	}
	cmd.AddCommand(ciRunCmd())
	return cmd
}

// ciRunCmd implements `de ci run -- <command...>`: create a dedicated ephemeral
// cluster for this run, run the wrapped command against it, and tear the cluster
// down on every exit path.
func ciRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run -- COMMAND [ARGS...]",
		Short: "Run a command against a dedicated ephemeral cluster, torn down on exit",
		Long: `Create a dedicated, per-run ephemeral cluster (devedge-ci-<runid>), run the
wrapped command with that cluster's context available via the environment
(DEVEDGE_KUBECONTEXT), and tear the cluster down when the command exits — on
success, failure, or interrupt. The wrapped command's exit code is propagated.

The user's global kube context is never changed.`,
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true, // everything after `run` is the wrapped command
		RunE: func(cmd *cobra.Command, args []string) error {
			args = stripLeadingDashDash(args)
			if len(args) == 0 {
				return fmt.Errorf("usage: de ci run -- COMMAND [ARGS...]")
			}
			ensurer := cluster.NewEnsurer(defaultProvider())
			code, err := runCI(context.Background(), ensurer, args, execRunner)
			if err != nil {
				return err
			}
			if code != 0 {
				// Propagate the wrapped command's exit code. Teardown has already
				// run (runCI's deferred cleanup fired before it returned), so it is
				// safe to exit directly here.
				os.Exit(code)
			}
			return nil
		},
	}
	return cmd
}

// ephemeralEnsurer is the slice of *cluster.Ensurer that runCI needs; an interface
// so the orchestration (create → run → always-teardown → propagate code) is
// unit-tested without a real cluster.
type ephemeralEnsurer interface {
	EnsureEphemeral(ctx context.Context) (cluster.ClusterTarget, error)
	Teardown(name string) error
}

// ciRunner executes the wrapped command against the ephemeral target, returning
// its exit code.
type ciRunner func(ctx context.Context, target cluster.ClusterTarget, args []string) (int, error)

// runCI is the CI wrapper's orchestration: ensure an ephemeral cluster, run the
// wrapped command, and ALWAYS tear the cluster down (success, failure, or signal)
// via deferred cleanup (FR-007). The wrapped exit code is returned, not turned
// into an error, so the caller can propagate it (FR-009).
func runCI(ctx context.Context, ensurer ephemeralEnsurer, args []string, run ciRunner) (int, error) {
	// Trap interrupts so teardown still runs on Ctrl-C / SIGTERM.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	target, err := ensurer.EnsureEphemeral(ctx)
	if err != nil {
		return 1, fmt.Errorf("create ephemeral cluster: %w", err)
	}
	defer func() {
		if terr := ensurer.Teardown(target.Name); terr != nil {
			fmt.Fprintf(os.Stderr, "warning: teardown ephemeral cluster %q: %v\n", target.Name, terr)
		}
	}()

	fmt.Printf("%s %s %s\n", colorLabel.Sprint("cluster:"), colorHost.Sprint(target.Name), colorLabel.Sprint("(ephemeral)"))
	return run(ctx, target, args)
}

// execRunner runs the wrapped command, scoping the ephemeral cluster's context to
// the child via the environment (DEVEDGE_* vars) without mutating the user's
// global kube context (FR-013/D8). It propagates the child's exit code.
func execRunner(ctx context.Context, target cluster.ClusterTarget, args []string) (int, error) {
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Env = append(os.Environ(),
		"DEVEDGE_ENV=ephemeral",
		"DEVEDGE_KUBECONTEXT="+target.KubeContext,
	)
	if err := c.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil // wrapped command ran and failed — propagate its code
		}
		return 1, fmt.Errorf("run %q: %w", args[0], err)
	}
	return 0, nil
}

// stripLeadingDashDash drops a leading "--" left by DisableFlagParsing.
func stripLeadingDashDash(args []string) []string {
	if len(args) > 0 && args[0] == "--" {
		return args[1:]
	}
	return args
}
