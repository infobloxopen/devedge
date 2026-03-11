package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/certs"
	"github.com/infobloxopen/devedge/internal/dns"
	"github.com/infobloxopen/devedge/internal/platform"
)

func installCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install devedge daemon and configure the system",
		RunE: func(cmd *cobra.Command, args []string) error {
			adapter := platform.Detect()
			fmt.Printf("Platform: %s\n", adapter.Name())

			// 1. Check mkcert.
			fmt.Print("Checking mkcert... ")
			if err := certs.EnsureCA(); err != nil {
				fmt.Println("installing CA")
				if err := certs.InstallCA(); err != nil {
					return fmt.Errorf("install mkcert CA: %w", err)
				}
			} else {
				fmt.Println("OK")
			}

			// 2. Install macOS resolver if applicable.
			fmt.Print("Configuring DNS... ")
			if err := dns.InstallResolverConfig("dev.test"); err != nil {
				fmt.Printf("skipped (%v)\n", err)
			} else {
				fmt.Println("OK")
			}

			// 3. Install daemon service.
			fmt.Print("Installing daemon service... ")
			if err := adapter.Install(); err != nil {
				return fmt.Errorf("install service: %w", err)
			}
			fmt.Println("OK")

			fmt.Println("\nInstallation complete. Run 'de start' to start the daemon.")
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
				icon := "PASS"
				if !r.Passed {
					icon = "FAIL"
					allPassed = false
				}
				fmt.Printf("  [%s] %-20s %s\n", icon, r.Name, r.Message)
			}
			if allPassed {
				fmt.Println("\nAll checks passed.")
			} else {
				fmt.Println("\nSome checks failed. Run 'de install' to fix.")
			}
		},
	}
}
