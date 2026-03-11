package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const launchAgentLabel = "io.devedge.daemon"

// DarwinAdapter manages devedged as a macOS LaunchAgent.
type DarwinAdapter struct{}

func (d *DarwinAdapter) Name() string { return "macOS (LaunchAgent)" }

func (d *DarwinAdapter) plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
}

func (d *DarwinAdapter) Install() error {
	binPath, err := findDevedged()
	if err != nil {
		return err
	}

	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".devedge", "logs")
	os.MkdirAll(logDir, 0755)

	data := plistData{
		Label:   launchAgentLabel,
		BinPath: binPath,
		LogDir:  logDir,
	}

	var buf strings.Builder
	if err := plistTmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("render plist: %w", err)
	}

	if err := os.WriteFile(d.plistPath(), []byte(buf.String()), 0644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	return nil
}

func (d *DarwinAdapter) Uninstall() error {
	d.Stop()
	path := d.plistPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (d *DarwinAdapter) Start() error {
	return exec.Command("launchctl", "load", d.plistPath()).Run()
}

func (d *DarwinAdapter) Stop() error {
	return exec.Command("launchctl", "unload", d.plistPath()).Run()
}

func (d *DarwinAdapter) IsRunning() (bool, error) {
	out, err := exec.Command("launchctl", "list").CombinedOutput()
	if err != nil {
		return false, err
	}
	return strings.Contains(string(out), launchAgentLabel), nil
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
	// Check common install locations.
	candidates := []string{
		"/usr/local/bin/devedged",
	}

	// Also check next to the current executable.
	if exe, err := os.Executable(); err == nil {
		candidates = append([]string{filepath.Join(filepath.Dir(exe), "devedged")}, candidates...)
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	// Fall back to PATH.
	if p, err := exec.LookPath("devedged"); err == nil {
		return p, nil
	}

	return "", fmt.Errorf("devedged binary not found; install it to /usr/local/bin/ or add to PATH")
}
