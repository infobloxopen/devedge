package platform

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/template"
)

const systemdServiceName = "devedged"

// LinuxAdapter manages devedged as a systemd user service.
type LinuxAdapter struct{}

func (l *LinuxAdapter) Name() string { return "Linux (systemd)" }

func (l *LinuxAdapter) unitPath() string {
	home, _ := os.UserHomeDir()
	return home + "/.config/systemd/user/" + systemdServiceName + ".service"
}

func (l *LinuxAdapter) Install() error {
	binPath, err := findDevedged()
	if err != nil {
		return err
	}

	dir := strings.TrimSuffix(l.unitPath(), "/"+systemdServiceName+".service")
	os.MkdirAll(dir, 0755)

	var buf strings.Builder
	if err := unitTmpl.Execute(&buf, map[string]string{"BinPath": binPath}); err != nil {
		return fmt.Errorf("render unit: %w", err)
	}

	if err := os.WriteFile(l.unitPath(), []byte(buf.String()), 0644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}

	return exec.Command("systemctl", "--user", "daemon-reload").Run()
}

func (l *LinuxAdapter) Uninstall() error {
	l.Stop()
	exec.Command("systemctl", "--user", "disable", systemdServiceName).Run()
	path := l.unitPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return exec.Command("systemctl", "--user", "daemon-reload").Run()
}

func (l *LinuxAdapter) Start() error {
	return exec.Command("systemctl", "--user", "start", systemdServiceName).Run()
}

func (l *LinuxAdapter) Stop() error {
	return exec.Command("systemctl", "--user", "stop", systemdServiceName).Run()
}

func (l *LinuxAdapter) IsRunning() (bool, error) {
	out, err := exec.Command("systemctl", "--user", "is-active", systemdServiceName).CombinedOutput()
	if err != nil {
		return false, nil // inactive is not an error
	}
	return strings.TrimSpace(string(out)) == "active", nil
}

var unitTmpl = template.Must(template.New("unit").Parse(`[Unit]
Description=Devedge local development edge daemon
After=network.target

[Service]
ExecStart={{.BinPath}}
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`))
