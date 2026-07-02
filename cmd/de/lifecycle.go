package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/certs"
	"github.com/infobloxopen/devedge/internal/dns"
	"github.com/infobloxopen/devedge/internal/makefrag"
	"github.com/infobloxopen/devedge/internal/platform"
)

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

			fmt.Printf("\n%s\n", colorSuccess.Sprint("Installation complete.")+colorLabel.Sprint(" Run 'de start' to start the daemon."))
			return nil
		},
	}
}

func startCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the devedge daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			adapter := platform.Detect()
			if err := adapter.Start(); err != nil {
				return fmt.Errorf("start daemon: %w", err)
			}
			fmt.Println("devedged started")
			return nil
		},
	}
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
