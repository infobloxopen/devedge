package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

// clusterTools are the CLI tools the daemon uses for cluster operations.
var clusterTools = []string{"helm", "kubectl", "k3d", "mkcert"}

// discoverToolEnv discovers the directories containing each required tool in
// the current PATH, builds a colon-joined PATH value that the daemon plist
// should carry, and returns a warnings string listing any tools not found.
// Falls back to a static minimal PATH when a tool's directory cannot be found.
// home and kubeconfig are passed through for the caller's use.
func discoverToolEnv(home, kubeconfig string) (toolPath, warnings string) {
	const fallback = "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
	seen := make(map[string]struct{})
	dirs := []string{}
	var missing []string
	for _, t := range clusterTools {
		p, err := exec.LookPath(t)
		if err != nil {
			missing = append(missing, t)
			continue
		}
		d := filepath.Dir(p)
		if _, ok := seen[d]; !ok {
			seen[d] = struct{}{}
			dirs = append(dirs, d)
		}
	}
	// Build PATH: discovered tool dirs first, then the static fallback dirs.
	for _, d := range strings.Split(fallback, ":") {
		if _, ok := seen[d]; !ok {
			seen[d] = struct{}{}
			dirs = append(dirs, d)
		}
	}
	toolPath = strings.Join(dirs, ":")
	if len(missing) > 0 {
		warnings = strings.Join(missing, ", ")
	}
	return toolPath, warnings
}

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

	// Resolve the invoking user's home dir. When called via `sudo de install`,
	// SUDO_USER is the original user; fall back to os.UserHomeDir() otherwise.
	home := invokerHome()
	devedgeHome := filepath.Join(home, ".devedge")
	logDir := filepath.Join(devedgeHome, "logs")
	os.MkdirAll(logDir, 0755)

	// Discover the kubeconfig path. Prefer $KUBECONFIG (the invoking user's
	// shell env, available via sudo passthrough); fall back to the default.
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	// Discover tool dirs from the invoking user's PATH; warn about missing tools
	// (non-blocking — the daemon won't work until tools are installed, but the
	// install itself should not fail for a missing tool).
	toolPath, warnings := discoverToolEnv(home, kubeconfig)
	if warnings != "" {
		fmt.Printf("Warning: tools not found in PATH (daemon may fail until installed): %s\n", warnings)
	}

	data := plistData{
		Label:       launchDaemonLabel,
		BinPath:     binPath,
		LogDir:      logDir,
		DevedgeHome: devedgeHome,
		ToolPATH:    toolPath,
		Home:        home,
		Kubeconfig:  kubeconfig,
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

// invokerHome returns the home directory of the user who invoked the process.
// Under `sudo de install` SUDO_USER is the original user; otherwise fall back
// to os.UserHomeDir.
func invokerHome() string {
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
		// Construct the home path for common macOS and Linux layouts.
		if strings.HasPrefix(sudoUser, "/") {
			return sudoUser
		}
		if home := os.Getenv("SUDO_HOME"); home != "" {
			return home
		}
		// macOS: /Users/<user>
		macHome := filepath.Join("/Users", sudoUser)
		if fileExists(macHome) {
			return macHome
		}
		// Linux: /home/<user>
		linuxHome := filepath.Join("/home", sudoUser)
		if fileExists(linuxHome) {
			return linuxHome
		}
	}
	home, _ := os.UserHomeDir()
	return home
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
	Label       string
	BinPath     string
	LogDir      string
	DevedgeHome string
	// ToolPATH, Home, Kubeconfig are written into EnvironmentVariables so the
	// daemon (which runs under launchd with a bare PATH and no HOME) can find
	// helm/kubectl/k3d/mkcert and read the user's kubeconfig (frictions #2–#4).
	ToolPATH   string
	Home       string
	Kubeconfig string
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
    <key>EnvironmentVariables</key>
    <dict>
        <key>DEVEDGE_HOME</key>
        <string>{{.DevedgeHome}}</string>
        <key>PATH</key>
        <string>{{.ToolPATH}}</string>
        <key>HOME</key>
        <string>{{.Home}}</string>
        <key>KUBECONFIG</key>
        <string>{{.Kubeconfig}}</string>
    </dict>
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
