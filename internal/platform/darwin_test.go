package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// renderPlistForTest renders the LaunchDaemon plist template with the given
// data, without writing to disk or touching launchctl.
func renderPlistForTest(t *testing.T, data plistData) string {
	t.Helper()
	var b strings.Builder
	if err := plistTmpl.Execute(&b, data); err != nil {
		t.Fatalf("render plist: %v", err)
	}
	return b.String()
}

// TestInstall_plistContainsEnvKeys verifies that the plist carries PATH, HOME,
// KUBECONFIG, and DEVEDGE_HOME in EnvironmentVariables, with the expected
// values (008 AC-001..003 / issue #9). Rendering the template directly avoids
// needing sudo or a real filesystem write.
func TestInstall_plistContainsEnvKeys(t *testing.T) {
	data := plistData{
		Label:       "io.devedge.daemon",
		BinPath:     "/usr/local/bin/devedged",
		LogDir:      "/Users/u/.devedge/logs",
		DevedgeHome: "/Users/u/.devedge",
		ToolPATH:    "/Users/u/.rd/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
		Home:        "/Users/u",
		Kubeconfig:  "/Users/u/.kube/config",
	}

	plist := renderPlistForTest(t, data)

	for _, key := range []string{"DEVEDGE_HOME", "PATH", "HOME", "KUBECONFIG"} {
		if !strings.Contains(plist, "<key>"+key+"</key>") {
			t.Errorf("plist missing EnvironmentVariables key %q\nplist:\n%s", key, plist)
		}
	}
	for _, val := range []string{data.ToolPATH, data.Home, data.Kubeconfig} {
		if !strings.Contains(plist, "<string>"+val+"</string>") {
			t.Errorf("plist missing value %q", val)
		}
	}
}

// TestDiscoverToolEnv_FindsToolsOnPATH puts fake tool binaries in a temp dir
// on PATH and verifies their directory leads the generated PATH.
func TestDiscoverToolEnv_FindsToolsOnPATH(t *testing.T) {
	toolDir := t.TempDir()
	for _, tool := range []string{"helm", "kubectl"} {
		if err := os.WriteFile(filepath.Join(toolDir, tool), []byte("#!/bin/sh\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", toolDir)

	home := t.TempDir()
	toolPath, missing := discoverToolEnv(home)

	dirs := strings.Split(toolPath, ":")
	if dirs[0] != toolDir {
		t.Errorf("discovered tool dir %q should lead the PATH, got %q", toolDir, toolPath)
	}
	for _, tool := range []string{"k3d", "docker", "mkcert"} {
		if !strings.Contains(missing, tool) {
			t.Errorf("missing list %q should mention %q", missing, tool)
		}
	}
	for _, tool := range []string{"helm", "kubectl"} {
		if strings.Contains(missing, tool) {
			t.Errorf("missing list %q should not mention found tool %q", missing, tool)
		}
	}
}

// TestDiscoverToolEnv_FallbackPATH verifies that a PATH with no tools still
// produces a usable superset PATH (well-known install dirs + system dirs)
// and reports every tool as missing.
func TestDiscoverToolEnv_FallbackPATH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	home := "/Users/u"
	toolPath, missing := discoverToolEnv(home)

	if toolPath == "" {
		t.Fatal("discoverToolEnv returned empty PATH; want at least the fallback dirs")
	}
	for _, dir := range []string{
		filepath.Join(home, ".rd", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
	} {
		if !strings.Contains(toolPath+":", dir+":") {
			t.Errorf("PATH %q missing fallback dir %q", toolPath, dir)
		}
	}
	for _, tool := range daemonTools {
		if !strings.Contains(missing, tool) {
			t.Errorf("missing list %q should mention %q", missing, tool)
		}
	}
}

// TestDiscoverToolEnv_NoDuplicateDirs verifies a tool found in a fallback
// dir does not produce a duplicate PATH entry.
func TestDiscoverToolEnv_NoDuplicateDirs(t *testing.T) {
	toolDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(toolDir, "helm"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, "kubectl"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolDir)

	toolPath, _ := discoverToolEnv(t.TempDir())

	seen := map[string]int{}
	for _, d := range strings.Split(toolPath, ":") {
		seen[d]++
		if seen[d] > 1 {
			t.Errorf("PATH %q contains duplicate dir %q", toolPath, d)
		}
	}
}

func TestInvokerHome_NoSudo(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := invokerHome(); got != home {
		t.Errorf("invokerHome = %q, want %q", got, home)
	}
}

func TestInvokerHome_SudoUserWithoutHomeDir_FallsBack(t *testing.T) {
	// A SUDO_USER whose /Users/<name> and /home/<name> do not exist must not
	// be trusted — invokerHome falls back to os.UserHomeDir.
	t.Setenv("SUDO_USER", "no-such-devedge-user")
	t.Setenv("SUDO_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := invokerHome(); got != home {
		t.Errorf("invokerHome = %q, want fallback %q", got, home)
	}
}
