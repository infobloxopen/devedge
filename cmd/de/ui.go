package main

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/daemon"
)

func uiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Open the devedge dashboard in a browser",
		RunE: func(cmd *cobra.Command, args []string) error {
			url := "http://" + daemon.DefaultTCPAddr() + "/ui"
			fmt.Printf("Opening dashboard at %s\n", url)
			return openBrowser(url)
		},
	}
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	default:
		return fmt.Errorf("unsupported platform for browser open; visit %s manually", url)
	}
}
