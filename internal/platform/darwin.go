package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const launchDaemonLabel = "io.devedge.daemon"

// launchDaemonPath is the system-level plist location (runs as root).
const launchDaemonPath = "/Library/LaunchDaemons/" + launchDaemonLabel + ".plist"

// DarwinAdapter manages devedged as a macOS LaunchDaemon (root).
type DarwinAdapter struct{}

func (d *DarwinAdapter) Name() string { return "macOS (LaunchDaemon)" }

func (d *DarwinAdapter) legacyPlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchDaemonLabel+".plist")
}

func (d *DarwinAdapter) Install() error {
	// Remove legacy LaunchAgent if it exists.
	if legacy := d.legacyPlistPath(); fileExists(legacy) {
		exec.Command("launchctl", "unload", legacy).Run()
		os.Remove(legacy)
	}

	binPath, err := findDevedged()
	if err != nil {
		return err
	}

	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".devedge", "logs")
	os.MkdirAll(logDir, 0755)

	data := plistData{
		Label:   launchDaemonLabel,
		BinPath: binPath,
		LogDir:  logDir,
	}

	var buf strings.Builder
	if err := plistTmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("render plist: %w", err)
	}

	if err := os.WriteFile(launchDaemonPath, []byte(buf.String()), 0644); err != nil {
		return fmt.Errorf("write plist (try with sudo): %w", err)
	}

	// Set up /etc/resolver for wildcard .test resolution
	if err := os.MkdirAll("/etc/resolver", 0755); err == nil {
		os.WriteFile("/etc/resolver/test", []byte("nameserver 127.0.0.1\n"), 0644)
	}

	return nil
}

func (d *DarwinAdapter) Uninstall() error {
	d.Stop()
	if err := os.Remove(launchDaemonPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (d *DarwinAdapter) Start() error {
	if _, err := os.Stat(launchDaemonPath); os.IsNotExist(err) {
		return fmt.Errorf("service not installed; run 'sudo de install' first")
	}
	out, err := exec.Command("launchctl", "load", launchDaemonPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl load: %w — %s", err, strings.TrimSpace(string(out)))
	}
	if s := strings.TrimSpace(string(out)); strings.Contains(s, "Load failed") || strings.Contains(s, ": error") {
		return fmt.Errorf("launchctl load: %s", s)
	}
	return nil
}

func (d *DarwinAdapter) Stop() error {
	if _, err := os.Stat(launchDaemonPath); os.IsNotExist(err) {
		return fmt.Errorf("service not installed; run 'sudo de install' first")
	}
	out, err := exec.Command("launchctl", "unload", launchDaemonPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl unload: %w — %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (d *DarwinAdapter) IsRunning() (bool, error) {
	out, err := exec.Command("launchctl", "list").CombinedOutput()
	if err != nil {
		return false, err
	}
	return strings.Contains(string(out), launchDaemonLabel), nil
}

type plistData struct {
	Label   string
	BinPath string
	LogDir  string
}

var plistTmpl = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.BinPath}}</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>{{.LogDir}}/devedged.out.log</string>
    <key>StandardErrorPath</key>
    <string>{{.LogDir}}/devedged.err.log</string>
</dict>
</plist>
`))

func findDevedged() (string, error) {
	candidates := []string{
		"/usr/local/bin/devedged",
	}

	if exe, err := os.Executable(); err == nil {
		candidates = append([]string{filepath.Join(filepath.Dir(exe), "devedged")}, candidates...)
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	if p, err := exec.LookPath("devedged"); err == nil {
		return p, nil
	}

	return "", fmt.Errorf("devedged binary not found; install it to /usr/local/bin/ or add to PATH")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
