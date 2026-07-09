package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/certs"
	"github.com/infobloxopen/devedge/internal/dns"
	"github.com/infobloxopen/devedge/internal/makefrag"
	"github.com/infobloxopen/devedge/internal/platform"
	"github.com/infobloxopen/devedge/internal/version"
)

// daemonBuild queries the running daemon's reported build (#56). running is false
// when no daemon answers (not running, or a build too old to report a version).
func daemonBuild(ctx context.Context) (running bool, ver, commit string) {
	st, err := newClient().Status(ctx)
	if err != nil {
		return false, "", ""
	}
	ver, _ = st["version"].(string)
	commit, _ = st["commit"].(string)
	return true, ver, commit
}

// sameDaemonBuild reports whether a running daemon's reported build matches this
// client's build. An empty version/commit (a daemon predating the /v1/status
// version field) never matches — it is treated as skew so `de start` replaces it.
func sameDaemonBuild(ver, commit string) bool {
	if ver == "" && commit == "" {
		return false
	}
	return ver == version.Version && commit == version.Commit
}

// daemonBuildLabel formats a daemon's reported build like version.String(),
// tolerating the empty fields an old daemon reports.
func daemonBuildLabel(ver, commit string) string {
	if ver == "" && commit == "" {
		return "an older build (no version reported)"
	}
	return fmt.Sprintf("devedge %s (%s)", ver, commit)
}

// warnDaemonSkew prints a best-effort warning when the running daemon is a
// different build than this client (#56). It is the diagnosis for the "route
// clobber": a stale daemon predating a routing change mis-registers routes with
// no other signal. Silent when no daemon is running or the builds match.
func warnDaemonSkew(out io.Writer) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	running, ver, commit := daemonBuild(ctx)
	if !running || sameDaemonBuild(ver, commit) {
		return
	}
	fmt.Fprintf(out, "%s the running devedged is %s but this client is %s.\n",
		colorWarning.Sprint("daemon version skew:"), daemonBuildLabel(ver, commit), version.String())
	fmt.Fprintln(out, "  a stale daemon can mis-register routes silently — run 'de start' to replace it.")
}

func installCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install devedge daemon and configure the system",
		RunE: func(cmd *cobra.Command, args []string) error {
			adapter := platform.Detect()
			fmt.Printf("%s %s\n", colorLabel.Sprint("Platform:"), adapter.Name())

			// 1. Check mkcert.
			fmt.Print("Checking mkcert... ")
			if err := certs.EnsureCA(); err != nil {
				colorWarning.Println("installing CA")
				if err := certs.InstallCA(); err != nil {
					return fmt.Errorf("install mkcert CA: %w", err)
				}
			} else {
				colorSuccess.Println("OK")
			}

			// 2. Record the resolved CAROOT for the daemon. The daemon runs
			// as root under launchd ($HOME=/var/root), so without this record
			// it cannot find the user's mkcert CA after a restart and silently
			// falls back to a self-signed CA that no browser trusts (issue #8).
			fmt.Print("Recording CA location... ")
			if root, err := certs.PersistCARoot(); err != nil {
				colorWarning.Printf("skipped (%v)\n", err)
			} else {
				colorSuccess.Printf("OK (%s)\n", root)
			}

			// 3. Install macOS resolver if applicable.
			fmt.Print("Configuring DNS... ")
			if err := dns.InstallResolverConfig("dev.test"); err != nil {
				colorWarning.Printf("skipped (%v)\n", err)
			} else {
				colorSuccess.Println("OK")
			}

			// 4. Install daemon service.
			fmt.Print("Installing daemon service... ")
			if err := adapter.Install(); err != nil {
				return fmt.Errorf("install service: %w", err)
			}
			colorSuccess.Println("OK")

			fmt.Printf("\n%s\n", colorSuccess.Sprint("Installation complete.")+colorLabel.Sprint(" Run 'sudo de start' to start the daemon."))
			return nil
		},
	}
}

func startCmd() *cobra.Command {
	var noReplace bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the devedge daemon",
		Long: `Start the devedge daemon.

If a daemon of a DIFFERENT build is already running — version skew after a client
upgrade, the cause of silent route mis-registration (#56) — 'de start' replaces it
(stop + start) so the running binary matches this client. Pass --no-replace to
only warn and leave the stale daemon running.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			adapter := platform.Detect()

			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
			running, dver, dcommit := daemonBuild(ctx)
			cancel()

			if running {
				if sameDaemonBuild(dver, dcommit) {
					fmt.Fprintf(out, "devedged already running (%s); up to date\n", version.String())
					return nil
				}
				fmt.Fprintf(out, "%s running %s, this client is %s\n",
					colorWarning.Sprint("daemon version skew:"), daemonBuildLabel(dver, dcommit), version.String())
				if noReplace {
					fmt.Fprintln(out, "leaving the stale daemon running (--no-replace); it may mis-register routes. Run 'de start' to replace it.")
					return nil
				}
				fmt.Fprint(out, "replacing stale daemon... ")
				if err := adapter.Stop(); err != nil {
					colorWarning.Fprintf(out, "stop failed: %v (continuing)\n", err)
				} else {
					colorSuccess.Fprintln(out, "stopped")
				}
			}

			if err := adapter.Start(); err != nil {
				return fmt.Errorf("start daemon: %w", err)
			}
			fmt.Fprintln(out, "devedged started")
			return nil
		},
	}
	cmd.Flags().BoolVar(&noReplace, "no-replace", false, "on daemon version skew, only warn; do not stop+start to replace the running daemon")
	return cmd
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the devedge daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			adapter := platform.Detect()
			if err := adapter.Stop(); err != nil {
				return fmt.Errorf("stop daemon: %w", err)
			}
			fmt.Println("devedged stopped")
			return nil
		},
	}
}

func doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check system health",
		Run: func(cmd *cobra.Command, args []string) {
			results := platform.RunDoctor()
			allPassed := true
			for _, r := range results {
				var icon string
				if r.Passed {
					icon = colorSuccess.Sprint("PASS")
				} else {
					icon = colorError.Sprint("FAIL")
					allPassed = false
				}
				fmt.Printf("  [%s] %-20s %s\n", icon, r.Name, r.Message)
			}
			// WS-023: in a service project, flag a managed Makefile fragment that
			// has gone stale or been hand-edited. Only reported when a fragment
			// exists in the current directory (a no-op elsewhere).
			if name, msg, ok, present := checkMakeFragment(); present {
				icon := colorSuccess.Sprint("PASS")
				if !ok {
					icon = colorError.Sprint("FAIL")
					allPassed = false
				}
				fmt.Printf("  [%s] %-20s %s\n", icon, name, msg)
			}
			if allPassed {
				fmt.Printf("\n%s\n", colorSuccess.Sprint("All checks passed."))
			} else {
				fmt.Printf("\n%s\n", colorError.Sprint("Some checks failed.")+" Run 'de install' to fix.")
			}
		},
	}
}

// checkMakeFragment inspects .devedge/make/devedge.mk in the current directory.
// present is false when there is no managed fragment (so doctor stays silent
// outside a synced service project). When present, ok reports whether it matches
// what `de sync` would write; a mismatch means stale or hand-edited.
func checkMakeFragment() (name, msg string, ok, present bool) {
	name = "make fragment"
	wd, err := os.Getwd()
	if err != nil {
		return name, "", false, false
	}
	b, err := os.ReadFile(makefrag.Path(wd))
	if err != nil || !makefrag.IsManaged(b) {
		return name, "", false, false
	}
	if makefrag.IsCurrent(b) {
		return name, makefrag.RelPath + " is up to date", true, true
	}
	return name, makefrag.RelPath + " is stale or hand-edited — run 'de sync' to regenerate", false, true
}
