package depruntime

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// nonIdent matches runs of characters not allowed in a SQL identifier / safe key.
var nonIdent = regexp.MustCompile(`[^a-z0-9]+`)

// sanitize lowercases s and collapses disallowed runs to a single underscore,
// trimming leading/trailing underscores. Deterministic.
func sanitize(s string) string {
	s = strings.ToLower(s)
	s = nonIdent.ReplaceAllString(s, "_")
	return strings.Trim(s, "_")
}

// bindingSlug is the deterministic, collision-avoided base identifier for a
// (service, dependency) pair, e.g. service "webhooks" + dep "db" -> "webhooks_db".
func bindingSlug(service, dependency string) string {
	svc := sanitize(service)
	dep := sanitize(dependency)
	switch {
	case svc == "" && dep == "":
		return "svc"
	case dep == "":
		return svc
	case svc == "":
		return dep
	}
	return svc + "_" + dep
}

// DatabaseName derives the Postgres database / Redis namespace base name.
func DatabaseName(service, dependency string) string { return bindingSlug(service, dependency) }

// RoleName derives the Postgres role / Redis ACL user.
func RoleName(service, dependency string) string { return bindingSlug(service, dependency) }

// KeyNamespace derives the Redis key prefix for a binding (e.g. "webhooks_db:").
func KeyNamespace(service, dependency string) string { return bindingSlug(service, dependency) + ":" }

// EnvVarName is the env var a service sets to reach a dependency. The base name
// is engine-conventional (DATABASE_URL / REDIS_URL); when a service declares more
// than one dependency of an engine, the dependency name disambiguates.
func EnvVarName(engine Engine, dependency string, ambiguous bool) string {
	base := "DATABASE_URL"
	if engine == EngineRedis {
		base = "REDIS_URL"
	}
	if !ambiguous {
		return base
	}
	return strings.ToUpper(sanitize(dependency)) + "_" + base
}

// newPassword returns a random URL-safe password for a binding.
func newPassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// NewBinding builds the isolation identity for a (service, dep). The password is
// freshly generated; callers persist it via the DSN file, not the daemon store.
func NewBinding(service string, d Dep) (Binding, error) {
	pw, err := newPassword()
	if err != nil {
		return Binding{}, err
	}
	b := Binding{
		Service:    service,
		Dependency: d.Name,
		Engine:     d.Engine,
		Database:   DatabaseName(service, d.Name),
		User:       RoleName(service, d.Name),
		Password:   pw,
	}
	if d.Engine == EngineRedis {
		b.KeyNamespace = KeyNamespace(service, d.Name)
		b.Database = "0" // logical DB index; isolation is via ACL user + key namespace
	}
	return b, nil
}
