package platform

import (
	"strings"
	"testing"
)

// renderPlist is a test helper to render a plist from structured data without
// writing to disk or touching launchctl.
func renderPlistForTest(data plistData) (string, error) {
	var b strings.Builder
	if err := plistTmpl.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// TestInstall_plistContainsEnvKeys verifies that DarwinAdapter.Install() writes
// PATH, HOME, and KUBECONFIG into the plist EnvironmentVariables dict (AC-001..003).
// We test this by rendering the template directly with known data so the test
// does not need sudo or a real filesystem write.
func TestInstall_plistContainsEnvKeys(t *testing.T) {
	data := plistData{
		Label:       "io.devedge.daemon",
		BinPath:     "/usr/local/bin/devedged",
		LogDir:      "/tmp/testhome/.devedge/logs",
		DevedgeHome: "/tmp/testhome/.devedge",
		ToolPATH:    "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
		Home:        "/tmp/testhome",
		Kubeconfig:  "/tmp/testhome/.kube/config",
	}

	plist, err := renderPlistForTest(data)
	if err != nil {
		t.Fatalf("renderPlist: %v", err)
	}

	for _, key := range []string{"PATH", "HOME", "KUBECONFIG", "DEVEDGE_HOME"} {
		if !strings.Contains(plist, "<key>"+key+"</key>") {
			t.Errorf("plist missing EnvironmentVariables key %q\nplist:\n%s", key, plist)
		}
	}

	if !strings.Contains(plist, data.ToolPATH) {
		t.Errorf("plist does not contain ToolPATH value %q", data.ToolPATH)
	}
	if !strings.Contains(plist, data.Home) {
		t.Errorf("plist does not contain Home value %q", data.Home)
	}
	if !strings.Contains(plist, data.Kubeconfig) {
		t.Errorf("plist does not contain Kubeconfig value %q", data.Kubeconfig)
	}
}

// TestInstall_plistFallbackPATH verifies that discoverToolEnv returns a non-empty
// PATH even when none of the named tools are present on PATH.
func TestInstall_plistFallbackPATH(t *testing.T) {
	// Override PATH to something with no tools; discoverToolEnv must fall back gracefully.
	t.Setenv("PATH", "/nonexistent")
	toolPath, warnings := discoverToolEnv("/tmp/home", "/tmp/home/.kube/config")
	if toolPath == "" {
		t.Error("discoverToolEnv returned empty ToolPATH; want at least the static fallback")
	}
	// warnings should list all four expected tools
	for _, tool := range []string{"helm", "kubectl", "k3d", "mkcert"} {
		if !strings.Contains(warnings, tool) {
			t.Errorf("warnings %q does not mention missing tool %q", warnings, tool)
		}
	}
}
