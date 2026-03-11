package main

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

func uiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Open the devedge dashboard in a browser",
		RunE: func(cmd *cobra.Command, args []string) error {
			// The dashboard is served by the daemon on the Unix socket.
			// Since browsers can't connect to Unix sockets directly, print
			// a note about accessing it through a proxy or adding a TCP
			// listener in the future.
			url := "http://localhost:15353/ui"
			fmt.Printf("Opening dashboard at %s\n", url)
			fmt.Println("(Requires devedged to also listen on TCP :15353 — coming soon)")
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
