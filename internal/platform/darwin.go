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

// daemonTools are the CLI tools the daemon execs at runtime (cluster and
// dependency operations). launchd starts daemons with the minimal system
// PATH (/usr/bin:/bin:/usr/sbin:/sbin), where none of these resolve when
// installed via Homebrew or Rancher Desktop — so `de install` captures the
// installing user's resolved locations into the plist PATH (issue #9).
var daemonTools = []string{"helm", "kubectl", "k3d", "docker", "mkcert"}

// discoverToolEnv discovers the directories containing each daemon tool on
// the invoking user's PATH and builds the colon-joined PATH value the daemon
// plist should carry: discovered tool dirs first, then a superset of
// well-known install locations, then the system dirs. It also returns a
// comma-joined list of tools that could not be found (informational — the
// fallback dirs usually still cover them once installed).
func discoverToolEnv(home string) (toolPath, missing string) {
	fallback := []string{
		filepath.Join(home, ".rd", "bin"), // Rancher Desktop shims
		"/opt/homebrew/bin",               // Homebrew (Apple Silicon)
		"/usr/local/bin",                  // Homebrew (Intel) / manual installs
		"/usr/bin", "/bin", "/usr/sbin", "/sbin",
	}

	seen := make(map[string]struct{})
	var dirs []string
	add := func(d string) {
		if _, ok := seen[d]; !ok {
			seen[d] = struct{}{}
			dirs = append(dirs, d)
		}
	}

	var notFound []string
	for _, tool := range daemonTools {
		p, err := exec.LookPath(tool)
		if err != nil {
			notFound = append(notFound, tool)
			continue
		}
		add(filepath.Dir(p))
	}
	for _, d := range fallback {
		add(d)
	}

	return strings.Join(dirs, ":"), strings.Join(notFound, ", ")
}

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

	// Resolve the invoking user's home dir: under `sudo de install` the
	// daemon state must live under the real user's home, not /var/root.
	// invokerHome derives it from SUDO_USER/SUDO_HOME, with macOS and Linux
	// layout fallbacks.
	home := invokerHome()
	devedgeHome := filepath.Join(home, ".devedge")
	logDir := filepath.Join(devedgeHome, "logs")
	os.MkdirAll(logDir, 0755)

	// Capture the kubeconfig path from the invoking user's environment
	// (available via sudo passthrough); fall back to the conventional
	// location under their home.
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	// Capture tool locations from the invoking user's PATH so the daemon —
	// which launchd starts with the bare system PATH — can exec them.
	// A missing tool is informational, not fatal: the install itself must
	// not fail, and the fallback dirs cover the tool once it is installed.
	toolPath, missing := discoverToolEnv(home)
	if missing != "" {
		fmt.Printf("warning: tools not found in PATH (daemon may fail to exec them until installed): %s\n", missing)
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
	// ToolPATH, Home, and Kubeconfig are written into EnvironmentVariables
	// so the daemon — which launchd starts with the minimal system PATH and
	// root's HOME — can exec helm/kubectl/k3d/docker/mkcert and read the
	// installing user's kube context (issue #9; frictions #2–#4).
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
