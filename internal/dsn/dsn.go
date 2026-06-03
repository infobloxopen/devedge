// Package dsn handles the DSN file + indirect env-var convention used by
// devedge to wire service dependencies at runtime.
//
// For each dependency devedge writes the real connection string to a
// mode-0600 file and exposes an indirect "hotload" DSN in an env var.
// The same pattern applies to every supported engine (postgres, redis).
package dsn

import (
	"fmt"
	"os"
	"path/filepath"
)

// Conn holds the resolved connection parameters for one service dependency.
type Conn struct {
	// Engine is the dependency type: "postgres" or "redis".
	Engine string
	// Host is the hostname or IP of the shared instance.
	Host string
	// Port is the TCP port of the shared instance.
	Port int
	// Database is the postgres database name, or the redis logical DB index
	// as a decimal string (e.g. "3").
	Database string
	// User is the provisioned role / ACL user.
	User string
	// Password is the provisioned credential.
	Password string
}

// RealDSN renders the real connection string for c.Engine.
//
//	postgres → postgres://<user>:<pw>@<host>:<port>/<database>
//	redis    → redis://<user>:<pw>@<host>:<port>/<dbIndex>
//
// Returns an error for any engine that is not "postgres" or "redis".
func RealDSN(c Conn) (string, error) {
	switch c.Engine {
	case "postgres":
		return fmt.Sprintf("postgres://%s:%s@%s:%d/%s",
			c.User, c.Password, c.Host, c.Port, c.Database), nil
	case "redis":
		return fmt.Sprintf("redis://%s:%s@%s:%d/%s",
			c.User, c.Password, c.Host, c.Port, c.Database), nil
	default:
		return "", fmt.Errorf("dsn: unknown engine %q", c.Engine)
	}
}

// IndirectEnv returns the hotload indirect DSN value that is exposed in an
// env var. It has the form:
//
//	fsnotify://<engine><absPath>
//
// where absPath is the absolute file path (which begins with '/'), so the
// result is "fsnotify://" + engine + absPath — e.g. for engine "postgres"
// and absPath "/Users/me/.devedge/services/svc/db.dsn" the result is
// "fsnotify://postgres/Users/me/.devedge/services/svc/db.dsn".
//
// absPath must be an absolute path (starts with '/').
func IndirectEnv(engine, absPath string) string {
	// absPath starts with '/', so concatenating engine + absPath naturally
	// gives "engine/rest/of/path" — the leading '/' of absPath becomes the
	// separator between the host segment and the path segment.
	return "fsnotify://" + engine + absPath
}

// FilePath returns the canonical DSN file path for a service dependency:
//
//	<baseDir>/services/<service>/<dependency>.dsn
//
// The result is cleaned (filepath.Clean). If baseDir is absolute the result
// is absolute; tests should pass t.TempDir() as baseDir.
func FilePath(baseDir, service, dependency string) string {
	return filepath.Clean(filepath.Join(baseDir, "services", service, dependency+".dsn"))
}

// DownStoreDir returns the canonical persisted down-migration store directory for
// a service dependency (006), a sibling of the DSN file:
//
//	<baseDir>/services/<service>/<dependency>.downstore
//
// The migration engine persists the applied up/down files here so a rollback
// survives the source tree changing (FR-012); `down --clean` removes it.
func DownStoreDir(baseDir, service, dependency string) string {
	return filepath.Clean(filepath.Join(baseDir, "services", service, dependency+".downstore"))
}

// WriteDSNFile atomically writes realDSN to path with mode 0600, creating
// all parent directories (mode 0700) as needed.
//
// It follows the temp-file-in-same-dir + os.Rename atomic-write pattern used
// throughout the codebase (see internal/render/traefik.go WriteRouteFile).
// No third-party packages are used.
func WriteDSNFile(path, realDSN string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("dsn: create parent dirs for %s: %w", path, err)
	}

	// Write to a sibling temp file so the rename is atomic on the same
	// filesystem. Use a name that is unlikely to collide.
	tmp := path + ".tmp"

	// WriteFile with 0600 so the temp file is never world-readable even
	// briefly; on rename the destination inherits the source permissions.
	if err := os.WriteFile(tmp, []byte(realDSN), 0600); err != nil {
		return fmt.Errorf("dsn: write temp file %s: %w", tmp, err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup
		return fmt.Errorf("dsn: rename %s → %s: %w", tmp, path, err)
	}

	// Ensure the final file is exactly 0600 regardless of the process umask.
	// (os.WriteFile respects umask; Chmod is unconditional.)
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("dsn: chmod %s: %w", path, err)
	}

	return nil
}

