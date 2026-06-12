package certs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetCAEnv makes CARoot resolution hermetic for a test: no env overrides,
// no install-time record, and a scratch $HOME/$PATH so neither the real
// user's mkcert CA nor the mkcert binary can leak into the result.
func resetCAEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DEVEDGE_CAROOT", "")
	t.Setenv("CAROOT", "")
	t.Setenv("SUDO_USER", "")
	t.Setenv("DEVEDGE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
}

// fakeCARoot creates a directory containing a rootCA.pem, mimicking a
// mkcert CAROOT.
func fakeCARoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFakeCert(t, filepath.Join(dir, "rootCA.pem"), []string{"devedge-test-ca"})
	return dir
}

func TestCARoot_EnvOverrides(t *testing.T) {
	for _, env := range []string{"DEVEDGE_CAROOT", "CAROOT"} {
		t.Run(env, func(t *testing.T) {
			resetCAEnv(t)
			dir := fakeCARoot(t)
			t.Setenv(env, dir)

			got, err := CARoot()
			if err != nil {
				t.Fatalf("CARoot: %v", err)
			}
			if got != dir {
				t.Errorf("CARoot = %q, want %q", got, dir)
			}
		})
	}
}

func TestCARoot_DevedgeOverrideWinsOverCAROOT(t *testing.T) {
	resetCAEnv(t)
	devedgeDir := fakeCARoot(t)
	mkcertDir := fakeCARoot(t)
	t.Setenv("DEVEDGE_CAROOT", devedgeDir)
	t.Setenv("CAROOT", mkcertDir)

	got, err := CARoot()
	if err != nil {
		t.Fatalf("CARoot: %v", err)
	}
	if got != devedgeDir {
		t.Errorf("CARoot = %q, want DEVEDGE_CAROOT dir %q", got, devedgeDir)
	}
}

func TestCARoot_EnvOverrideWithoutCA_Errors(t *testing.T) {
	resetCAEnv(t)
	empty := t.TempDir() // no rootCA.pem inside
	t.Setenv("DEVEDGE_CAROOT", empty)

	_, err := CARoot()
	if err == nil {
		t.Fatal("expected error for explicit override without rootCA.pem, got nil")
	}
	if !strings.Contains(err.Error(), "DEVEDGE_CAROOT") {
		t.Errorf("error %q should name the override variable", err)
	}
}

func TestCARoot_UsesInstallTimeRecord(t *testing.T) {
	resetCAEnv(t)
	caDir := fakeCARoot(t)
	record := CARootRecordPath()
	if err := os.WriteFile(record, []byte(caDir+"\n"), 0644); err != nil {
		t.Fatalf("write record: %v", err)
	}

	got, err := CARoot()
	if err != nil {
		t.Fatalf("CARoot: %v", err)
	}
	if got != caDir {
		t.Errorf("CARoot = %q, want recorded dir %q", got, caDir)
	}
}

func TestCARoot_StaleRecordFallsThrough(t *testing.T) {
	resetCAEnv(t)
	record := CARootRecordPath()
	if err := os.WriteFile(record, []byte("/nonexistent/mkcert\n"), 0644); err != nil {
		t.Fatalf("write record: %v", err)
	}

	// The recorded CA is gone and no live CA exists anywhere else in this
	// hermetic environment, so resolution must fail — not return the stale dir.
	got, err := CARoot()
	if err == nil {
		t.Fatalf("expected error, got %q", got)
	}
}

func TestCARoot_NothingFound_Errors(t *testing.T) {
	resetCAEnv(t)
	_, err := CARoot()
	if err == nil {
		t.Fatal("expected error when no CA exists anywhere")
	}
	if !strings.Contains(err.Error(), "mkcert -install") {
		t.Errorf("error %q should point the user at 'mkcert -install'", err)
	}
}

func TestResolveCARoot_IgnoreRecord(t *testing.T) {
	resetCAEnv(t)
	caDir := fakeCARoot(t)
	if err := os.WriteFile(CARootRecordPath(), []byte(caDir+"\n"), 0644); err != nil {
		t.Fatalf("write record: %v", err)
	}

	got, err := resolveCARoot(false)
	if err != nil || got != caDir {
		t.Fatalf("resolveCARoot(false) = %q, %v; want %q via record", got, err, caDir)
	}

	// With ignoreRecord the record must be skipped, and nothing else
	// resolves in the hermetic environment.
	if got, err := resolveCARoot(true); err == nil {
		t.Fatalf("resolveCARoot(true) = %q, want error (record must be ignored)", got)
	}
}

func TestPersistCARoot_RecordsAndRoundTrips(t *testing.T) {
	resetCAEnv(t)
	caDir := fakeCARoot(t)
	t.Setenv("DEVEDGE_CAROOT", caDir)

	root, err := PersistCARoot()
	if err != nil {
		t.Fatalf("PersistCARoot: %v", err)
	}
	if root != caDir {
		t.Errorf("PersistCARoot = %q, want %q", root, caDir)
	}

	data, err := os.ReadFile(CARootRecordPath())
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != caDir {
		t.Errorf("record content = %q, want %q", got, caDir)
	}

	// The daemon resolves via the record alone (no env override, different
	// $HOME than the installing user's).
	t.Setenv("DEVEDGE_CAROOT", "")
	got, err := CARoot()
	if err != nil {
		t.Fatalf("CARoot after persist: %v", err)
	}
	if got != caDir {
		t.Errorf("CARoot after persist = %q, want %q", got, caDir)
	}
}

func TestPersistCARoot_RefreshesStaleRecord(t *testing.T) {
	resetCAEnv(t)
	if err := os.WriteFile(CARootRecordPath(), []byte("/nonexistent/mkcert\n"), 0644); err != nil {
		t.Fatalf("write record: %v", err)
	}
	caDir := fakeCARoot(t)
	t.Setenv("CAROOT", caDir)

	root, err := PersistCARoot()
	if err != nil {
		t.Fatalf("PersistCARoot: %v", err)
	}
	if root != caDir {
		t.Errorf("PersistCARoot = %q, want freshly resolved %q (not the stale record)", root, caDir)
	}
	data, _ := os.ReadFile(CARootRecordPath())
	if got := strings.TrimSpace(string(data)); got != caDir {
		t.Errorf("record content = %q, want refreshed %q", got, caDir)
	}
}

func TestPersistCARoot_NoCA_Errors(t *testing.T) {
	resetCAEnv(t)
	if _, err := PersistCARoot(); err == nil {
		t.Fatal("expected error when no CA is resolvable")
	}
	if _, err := os.Stat(CARootRecordPath()); !os.IsNotExist(err) {
		t.Errorf("no record should be written on failure, stat err = %v", err)
	}
}
