package dsn_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/infobloxopen/devedge/internal/dsn"
)

// ---------------------------------------------------------------------------
// RealDSN
// ---------------------------------------------------------------------------

func TestRealDSN_Postgres(t *testing.T) {
	c := dsn.Conn{
		Engine:   "postgres",
		Host:     "postgres.dev.test",
		Port:     5432,
		Database: "mydb",
		User:     "myuser",
		Password: "s3cr3t",
	}
	got, err := dsn.RealDSN(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "postgres://myuser:s3cr3t@postgres.dev.test:5432/mydb"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRealDSN_Redis(t *testing.T) {
	c := dsn.Conn{
		Engine:   "redis",
		Host:     "redis.dev.test",
		Port:     6379,
		Database: "3",
		User:     "acluser",
		Password: "r3dis",
	}
	got, err := dsn.RealDSN(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "redis://acluser:r3dis@redis.dev.test:6379/3"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRealDSN_UnknownEngine(t *testing.T) {
	c := dsn.Conn{Engine: "mysql"}
	_, err := dsn.RealDSN(c)
	if err == nil {
		t.Fatal("expected error for unknown engine, got nil")
	}
}

// ---------------------------------------------------------------------------
// IndirectEnv
// ---------------------------------------------------------------------------

func TestIndirectEnv_Postgres(t *testing.T) {
	got := dsn.IndirectEnv("postgres", "/Users/me/.devedge/services/webhooks/db.dsn")
	want := "fsnotify://postgres/Users/me/.devedge/services/webhooks/db.dsn"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestIndirectEnv_Redis(t *testing.T) {
	got := dsn.IndirectEnv("redis", "/a/b/c.dsn")
	want := "fsnotify://redis/a/b/c.dsn"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The contract example: leading / of absolute path becomes the / separator
// after the host, so the result is "fsnotify://postgres/a/b/c.dsn" (not
// "fsnotify://postgres//a/b/c.dsn").
func TestIndirectEnv_AbsPathPreserved(t *testing.T) {
	in := "/a/b/c.dsn"
	got := dsn.IndirectEnv("postgres", in)
	want := "fsnotify://postgres/a/b/c.dsn"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// FilePath
// ---------------------------------------------------------------------------

func TestFilePath(t *testing.T) {
	got := dsn.FilePath("/home/user/.devedge", "webhooks", "db")
	want := "/home/user/.devedge/services/webhooks/db.dsn"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFilePath_Cleaned(t *testing.T) {
	// Extra slashes should be cleaned.
	got := dsn.FilePath("/base//dir", "svc", "dep")
	want := "/base/dir/services/svc/dep.dsn"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// WriteDSNFile
// ---------------------------------------------------------------------------

func TestWriteDSNFile_Contents(t *testing.T) {
	base := t.TempDir()
	path := dsn.FilePath(base, "mysvc", "mydb")
	realDSN := "postgres://u:p@host:5432/db"

	if err := dsn.WriteDSNFile(path, realDSN); err != nil {
		t.Fatalf("WriteDSNFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != realDSN {
		t.Errorf("file contents = %q, want %q", got, realDSN)
	}
}

func TestWriteDSNFile_Mode(t *testing.T) {
	base := t.TempDir()
	path := dsn.FilePath(base, "mysvc", "mydb")

	if err := dsn.WriteDSNFile(path, "redis://u:p@host:6379/0"); err != nil {
		t.Fatalf("WriteDSNFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("file mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestWriteDSNFile_ParentDirsCreated(t *testing.T) {
	base := t.TempDir()
	// Use a nested path that does not yet exist.
	path := filepath.Join(base, "services", "deep", "nested", "dep.dsn")

	if err := dsn.WriteDSNFile(path, "redis://u:p@host:6379/1"); err != nil {
		t.Fatalf("WriteDSNFile: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not found after write: %v", err)
	}
}

func TestWriteDSNFile_AtomicOverwrite(t *testing.T) {
	base := t.TempDir()
	path := dsn.FilePath(base, "mysvc", "pg")

	first := "postgres://u:p1@host:5432/db"
	if err := dsn.WriteDSNFile(path, first); err != nil {
		t.Fatalf("first write: %v", err)
	}

	second := "postgres://u:p2@host:5432/db"
	if err := dsn.WriteDSNFile(path, second); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != second {
		t.Errorf("after overwrite contents = %q, want %q", got, second)
	}

	// Mode must still be 0600 after overwrite.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("file mode after overwrite = %o, want 0600", info.Mode().Perm())
	}
}
