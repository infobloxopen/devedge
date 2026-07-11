package compose

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"

	"github.com/infobloxopen/devedge/pkg/config"
)

// DefaultSDKVersion is the devedge-sdk version the generated composed binary
// requires when it cannot be derived from a member module (the published case
// with no local --path). It tracks the version `de` itself builds against; a real
// composition SHOULD derive the pin from its members' go.mod (see Generate).
const DefaultSDKVersion = "v0.61.0"

// SDKVersion is the fallback devedge-sdk pin for the generated composed binary.
// Kept for callers/tests; Generate now DERIVES the pin from the member modules'
// own go.mod when they are available locally (`de compose add --path`), so a
// composition of v0.51.0 members pins v0.51.0 rather than this default.
const SDKVersion = DefaultSDKVersion

// localReplaceVersion is the require-version Go uses for a module satisfied by a
// local `replace` directive (the zero pseudo-version). `de compose add --path`
// pins members here and adds the replace so `go mod tidy` resolves them from disk
// instead of trying to fetch an unpublished path.
const localReplaceVersion = "v0.0.0-00010101000000-000000000000"

// gormtxModule is the separate SDK sub-module the generated host imports for its
// host-run per-module migration (MigrateModule).
const gormtxModule = "github.com/infobloxopen/devedge-sdk/persistence/gormtx"

// postgresDriverModule/Version is the GORM Postgres driver the generated host
// requires when the composition declares `database.engine: postgres`. `go mod
// tidy` (the documented next step after `de compose build`) reconciles it against
// the composed graph; the explicit require makes the generated go.mod build
// without a tidy and pins a version compatible with the SDK's gorm.
const (
	postgresDriverModule  = "gorm.io/driver/postgres"
	postgresDriverVersion = "v1.6.0"
)

// usesPostgres reports whether a composition declares a Postgres shared database,
// which switches the generated host from the SQLite-only dev path to a
// scheme-branched dialector (and adds the Postgres driver to its go.mod).
func usesPostgres(c *config.Composition) bool {
	return c.Spec.Database != nil && strings.EqualFold(c.Spec.Database.Engine, "postgres")
}

// dsnEnvVar returns the environment variable the generated host reads its DSN
// from: the composition's declared database.dsnRef, or "<PROJECT>_DSN" when none
// is declared.
func dsnEnvVar(c *config.Composition) string {
	if c.Spec.Database != nil && c.Spec.Database.DSNRef != "" {
		return c.Spec.Database.DSNRef
	}
	return envKey(c.Project()) + "_DSN"
}

// GeneratedFiles is the output of Generate: the rendered, ready-to-write files
// for a composed binary (paths are relative to the composition's project dir).
type GeneratedFiles struct {
	// MainGo is cmd/<name>/main.go — the host entrypoint importing the modules.
	MainGo string
	// GoMod is cmd/<name>/go.mod — the composed binary's module file.
	GoMod string
	// Lock is cmd/<name>/composition.lock — the pinned member/SDK/toolchain set.
	Lock string
	// Dir is the relative output directory ("cmd/<name>").
	Dir string
	// SDK is the devedge-sdk version the generated binary pins (derived from the
	// members when available, else DefaultSDKVersion).
	SDK string
}

// GenerateOptions carries the on-disk locations `de compose build` needs to
// resolve local members (`--path`): the composition file's directory and the base
// dir the generated cmd/<name> tree is written under. The go.mod `replace` target
// is computed relative to the generated go.mod, and each local member's own
// go.mod is read to derive the module path + its devedge-sdk pin.
type GenerateOptions struct {
	// CompositionDir is the directory of the composition file. A member's stored
	// --path is resolved relative to it.
	CompositionDir string
	// OutBaseDir is the base dir the generated cmd/<name> tree is written under
	// (defaults to CompositionDir). The replace path is relative to
	// filepath.Join(OutBaseDir, "cmd", name).
	OutBaseDir string
}

// resolvedRef is a ModuleRef enriched with the facts a valid go.mod needs: the Go
// MODULE path (not the package import path) to require/replace, the require
// version, an optional local replace target, and the member's own devedge-sdk pin.
type resolvedRef struct {
	ModuleRef
	// GoModulePath is the module path to write in require/replace (from the local
	// member's go.mod when --path is set, else the package import path).
	GoModulePath string
	// RequireVersion is the version token for the require line (a real pin, or the
	// local pseudo-version when the member is resolved by a replace).
	RequireVersion string
	// ReplaceTarget is the relative path a `replace` points at (empty when the
	// member is published/pinned, not local).
	ReplaceTarget string
	// SDK is the devedge-sdk version this member's own go.mod pins (empty when not
	// locally resolvable).
	SDK string
}

// Generate renders the composed-binary sources for a composition. It reads each
// local member's go.mod (when `de compose add --path` recorded one) to derive the
// composed go.mod's replace directives and its devedge-sdk pin, so the generated
// host builds against the SAME SDK its members do.
//
// goVersion is the Go toolchain version line for the generated go.mod (e.g.
// "1.26.0"); modulePath is the Go module path of the generated binary.
func Generate(c *config.Composition, goVersion, modulePath string, opts GenerateOptions) (GeneratedFiles, error) {
	refs, err := ResolveModuleRefs(c)
	if err != nil {
		return GeneratedFiles{}, err
	}
	name := c.Project()
	dir := "cmd/" + name

	outBase := opts.OutBaseDir
	if outBase == "" {
		outBase = opts.CompositionDir
	}
	cmdDir := filepath.Join(outBase, "cmd", name)

	resolved, sdk, err := resolveRefs(refs, opts.CompositionDir, cmdDir)
	if err != nil {
		return GeneratedFiles{}, err
	}

	return GeneratedFiles{
		MainGo: renderMainGo(c, refs),
		GoMod:  renderGoMod(modulePath, goVersion, sdk, resolved, usesPostgres(c)),
		Lock:   renderLock(c, refs, goVersion, sdk),
		Dir:    dir,
		SDK:    sdk,
	}, nil
}

// resolveRefs enriches each ModuleRef into a resolvedRef and derives the composed
// binary's devedge-sdk pin from the members. A member with a local Path has its
// go.mod read for its module path + SDK pin and is required at the local
// pseudo-version behind a replace; a published member is required at its pinned
// version (an unpinned published member is an error — no version to build).
func resolveRefs(refs []ModuleRef, compositionDir, cmdDir string) ([]resolvedRef, string, error) {
	// filepath.Rel needs both operands absolute (or both relative); the composition
	// dir + generated cmd dir may arrive relative (e.g. "." for the default file),
	// so anchor everything to absolute before computing the replace target.
	absCmdDir, err := filepath.Abs(cmdDir)
	if err != nil {
		return nil, "", fmt.Errorf("resolve output dir: %w", err)
	}
	absCompDir, err := filepath.Abs(compositionDir)
	if err != nil {
		return nil, "", fmt.Errorf("resolve composition dir: %w", err)
	}
	out := make([]resolvedRef, 0, len(refs))
	var sdks []string
	for _, r := range refs {
		rr := resolvedRef{ModuleRef: r, GoModulePath: r.ImportPath, RequireVersion: r.Version}
		if r.Path != "" {
			memberDir := r.Path
			if !filepath.IsAbs(memberDir) {
				memberDir = filepath.Join(absCompDir, r.Path)
			}
			modPath, sdk, err := readMemberGoMod(memberDir)
			if err != nil {
				return nil, "", fmt.Errorf("member %q: %w", r.Name, err)
			}
			rel, err := filepath.Rel(absCmdDir, memberDir)
			if err != nil {
				return nil, "", fmt.Errorf("member %q: relative path: %w", r.Name, err)
			}
			rr.GoModulePath = modPath
			rr.RequireVersion = localReplaceVersion
			rr.ReplaceTarget = filepath.ToSlash(rel)
			rr.SDK = sdk
			if sdk != "" {
				sdks = append(sdks, sdk)
			}
		} else if r.Version == "" {
			return nil, "", fmt.Errorf("member %q has no version: pin it with `de compose add %s@<version>` or point at a local checkout with `de compose add %s --path <dir>`", r.Name, r.ImportPath, r.ImportPath)
		}
		out = append(out, rr)
	}
	return out, maxSDK(sdks), nil
}

// readMemberGoMod reads a local member module's go.mod, returning its module path
// and the devedge-sdk version it requires (empty if it does not require the SDK).
func readMemberGoMod(memberDir string) (modulePath, sdk string, err error) {
	data, err := os.ReadFile(filepath.Join(memberDir, "go.mod"))
	if err != nil {
		return "", "", fmt.Errorf("read go.mod (is --path a Go module root?): %w", err)
	}
	mf, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return "", "", fmt.Errorf("parse go.mod: %w", err)
	}
	if mf.Module == nil || mf.Module.Mod.Path == "" {
		return "", "", fmt.Errorf("go.mod has no module path")
	}
	for _, req := range mf.Require {
		if req.Mod.Path == "github.com/infobloxopen/devedge-sdk" {
			sdk = req.Mod.Version
			break
		}
	}
	return mf.Module.Mod.Path, sdk, nil
}

// maxSDK returns the highest devedge-sdk version among the members (semver order),
// or DefaultSDKVersion when none was derivable.
func maxSDK(versions []string) string {
	best := ""
	for _, v := range versions {
		if v == "" || !semver.IsValid(v) {
			continue
		}
		if best == "" || semver.Compare(v, best) > 0 {
			best = v
		}
	}
	if best == "" {
		return DefaultSDKVersion
	}
	return best
}

// renderMainGo renders cmd/<name>/main.go: a HOST that opens ONE shared database,
// builds each member module over it via the module's NewModule(db) seam, runs a
// host-owned union migration, and serves them via servicekit.Run — STATIC
// composition (the modules are imported, not loaded as plugins).
//
// WS-012 contract (Run 18 finding 079): a composable module exposes a uniform,
// resource-agnostic NewModule(db) + Models() seam, so this generated host names no
// member's repository or model. "Module owns domain, host owns process": the host
// opens the shared connection; each module builds its own typed repository from it.
func renderMainGo(c *config.Composition, refs []ModuleRef) string {
	pgEnabled := usesPostgres(c)
	dsnRef := dsnEnvVar(c)
	var b strings.Builder
	b.WriteString("// Code generated by `de compose build`; DO NOT EDIT.\n")
	b.WriteString("//\n")
	fmt.Fprintf(&b, "// Composed host for %q (WS-012). It IMPORTS the member service modules, opens\n", c.Project())
	b.WriteString("// ONE shared database, builds each module over it via the module's NewModule(db)\n")
	b.WriteString("// seam, and runs them in ONE process via servicekit.Run — static composition, no\n")
	b.WriteString("// Go plugins. Regenerate after editing the composition (do not hand-edit).\n")
	b.WriteString("package main\n\n")

	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	b.WriteString("\t\"log\"\n")
	b.WriteString("\t\"log/slog\"\n")
	b.WriteString("\t\"os\"\n")
	b.WriteString("\t\"os/signal\"\n")
	if pgEnabled {
		b.WriteString("\t\"strings\"\n")
	}
	b.WriteString("\t\"syscall\"\n\n")
	b.WriteString("\t\"github.com/glebarez/sqlite\"\n")
	if pgEnabled {
		b.WriteString("\t\"gorm.io/driver/postgres\"\n")
	}
	b.WriteString("\t\"gorm.io/gorm\"\n")
	b.WriteString("\t\"gorm.io/gorm/logger\"\n\n")
	b.WriteString("\t\"github.com/infobloxopen/devedge-sdk/authz\"\n")
	b.WriteString("\t\"github.com/infobloxopen/devedge-sdk/authz/grpcauthz\"\n")
	b.WriteString("\t\"github.com/infobloxopen/devedge-sdk/persistence/gormtx\"\n")
	b.WriteString("\t\"github.com/infobloxopen/devedge-sdk/servicekit\"\n")
	for _, r := range refs {
		fmt.Fprintf(&b, "\t%s %q\n", r.Alias, r.ImportPath)
	}
	b.WriteString(")\n\n")

	b.WriteString("func main() {\n")
	b.WriteString("\t// The HOST owns the process: signals, and the ONE shared database every\n")
	b.WriteString("\t// module namespaces itself within.\n")
	b.WriteString("\tctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)\n")
	b.WriteString("\tdefer stop()\n\n")

	if pgEnabled {
		fmt.Fprintf(&b, "\t// The composition declares database.engine: postgres. A postgres:// (or\n")
		fmt.Fprintf(&b, "\t// postgresql://) DSN in %s selects the Postgres dialector; an empty %s\n", dsnRef, dsnRef)
		b.WriteString("\t// falls back to in-memory SQLite so the composed binary still runs out of the\n")
		b.WriteString("\t// box in dev. On Postgres each module migrates into its own schema (below).\n")
	} else {
		b.WriteString("\t// Dev default: in-memory SQLite (pure-Go, no CGo) so the composed binary runs\n")
		fmt.Fprintf(&b, "\t// out of the box. Set %s to a postgres:// URL and declare database.engine:\n", dsnRef)
		b.WriteString("\t// postgres to build a Postgres host (versioned migrations then apply through\n")
		b.WriteString("\t// module/migrations).\n")
	}
	fmt.Fprintf(&b, "\tdsn := os.Getenv(%q)\n", dsnRef)
	b.WriteString("\tif dsn == \"\" {\n")
	b.WriteString("\t\tdsn = \"file::memory:?cache=shared\"\n")
	b.WriteString("\t}\n")
	if pgEnabled {
		b.WriteString("\tdb, err := gorm.Open(dialectorFor(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})\n")
	} else {
		b.WriteString("\tdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})\n")
	}
	b.WriteString("\tif err != nil {\n")
	fmt.Fprintf(&b, "\t\tlog.Fatalf(%q, err)\n", c.Project()+": open db: %v")
	b.WriteString("\t}\n\n")

	b.WriteString("\t// Each module builds its OWN repository from the shared connection (module\n")
	b.WriteString("\t// owns domain); the host just hands over the connection.\n")
	b.WriteString("\tmodules := []servicekit.Module{\n")
	for _, r := range refs {
		fmt.Fprintf(&b, "\t\t%s.NewModule(db),\n", r.Alias)
	}
	b.WriteString("\t}\n\n")

	b.WriteString("\t// Host-run migration (WS-012 P2), namespaced PER MODULE: the host calls this\n")
	b.WriteString("\t// once per module keyed by the module's descriptor ID, so we migrate ONLY that\n")
	b.WriteString("\t// module's models into its own schema/prefix. Migrating the UNION into every\n")
	b.WriteString("\t// module's namespace would give each schema every other module's tables and\n")
	b.WriteString("\t// defeat schema-preferred isolation. On Postgres the versioned SQL in each\n")
	b.WriteString("\t// module/migrations is the schema-of-record (run through this same seam).\n")
	b.WriteString("\tmodelsByModule := map[string][]any{\n")
	for i, r := range refs {
		fmt.Fprintf(&b, "\t\tmodules[%d].Descriptor().ID: %s.Models(),\n", i, r.Alias)
	}
	b.WriteString("\t}\n")
	b.WriteString("\tmigrate := func(ctx context.Context, ns servicekit.DatabaseNamespace, d servicekit.DatabaseDescriptor) error {\n")
	b.WriteString("\t\treturn gormtx.MigrateModule(ctx, db, gormtx.MigrateOptions{\n")
	b.WriteString("\t\t\tNamespace:        ns,\n")
	b.WriteString("\t\t\tDomainModels:     modelsByModule[ns.ModuleID],\n")
	b.WriteString("\t\t\tSkipAdvisoryLock: db.Dialector.Name() != \"postgres\",\n")
	b.WriteString("\t\t})\n")
	b.WriteString("\t}\n\n")

	b.WriteString("\t// Dev authorizer + principal (the same default the standalone scaffold ships):\n")
	b.WriteString("\t// grant group:admin every verb; a caller is authorized by sending\n")
	b.WriteString("\t// `account-id: <tenant>` + `groups: admin`. Replace BOTH in production.\n")
	b.WriteString("\tauthorizer := authz.NewDevAuthorizer(authz.Grant{\n")
	b.WriteString("\t\tTenant:   \"*\",\n")
	b.WriteString("\t\tSubjects: []string{\"group:admin\"},\n")
	b.WriteString("\t\tVerbs:    []authz.Verb{\"*\"},\n")
	b.WriteString("\t\tResource: \"*\",\n")
	b.WriteString("\t})\n\n")

	b.WriteString("\thc := servicekit.HostConfig{\n")
	b.WriteString("\t\tModules:       modules,\n")
	if g := c.Spec.Runtime.GRPC; g != "" {
		fmt.Fprintf(&b, "\t\tGRPCAddr:      %q,\n", g)
	}
	if h := c.Spec.Runtime.HTTP; h != "" {
		fmt.Fprintf(&b, "\t\tHTTPAddr:      %q,\n", h)
	}
	b.WriteString("\t\tMigrate:       migrate,\n")
	b.WriteString("\t\tAuthorizer:    authorizer,\n")
	b.WriteString("\t\tPrincipalFunc: grpcauthz.DevPrincipalFunc(),\n")
	b.WriteString("\t\tLogger:        slog.Default(),\n")
	b.WriteString("\t\tContext:       ctx,\n")

	if d := c.Spec.Database; d != nil {
		b.WriteString("\t\tDatabase: &servicekit.DatabaseConfig{\n")
		fmt.Fprintf(&b, "\t\t\tEngine: %q,\n", d.Engine)
		if d.Isolation != "" {
			fmt.Fprintf(&b, "\t\t\tDefaultIsolation: servicekit.IsolationPolicy(%q),\n", d.Isolation)
		}
		b.WriteString("\t\t},\n")
	}

	// Per-module failure policies (proposal §5.9), emitted only when any module
	// declares one. Keyed by module name, deterministically ordered.
	policies := map[string]string{}
	for _, m := range c.Spec.Modules {
		if m.FailurePolicy != "" {
			policies[m.Name] = m.FailurePolicy
		}
	}
	if len(policies) > 0 {
		b.WriteString("\t\tFailurePolicies: map[string]servicekit.FailurePolicy{\n")
		names := make([]string, 0, len(policies))
		for n := range policies {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(&b, "\t\t\t%q: servicekit.FailurePolicy(%q),\n", n, policies[n])
		}
		b.WriteString("\t\t},\n")
	}

	b.WriteString("\t}\n")
	fmt.Fprintf(&b, "\tlog.Printf(%q, hc.GRPCAddr, hc.HTTPAddr)\n", c.Project()+": serving gRPC on %s, HTTP on %s")
	b.WriteString("\tif err := servicekit.Run(hc); err != nil {\n")
	b.WriteString("\t\tlog.Fatal(err)\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n")

	if pgEnabled {
		b.WriteString("\n")
		b.WriteString("// dialectorFor selects the GORM dialector by DSN scheme: a postgres:// (or\n")
		b.WriteString("// postgresql://) DSN uses the Postgres driver; anything else — including the\n")
		b.WriteString("// dev in-memory default — uses pure-Go SQLite.\n")
		b.WriteString("func dialectorFor(dsn string) gorm.Dialector {\n")
		b.WriteString("\tif isPostgresDSN(dsn) {\n")
		b.WriteString("\t\treturn postgres.Open(dsn)\n")
		b.WriteString("\t}\n")
		b.WriteString("\treturn sqlite.Open(dsn)\n")
		b.WriteString("}\n\n")
		b.WriteString("// isPostgresDSN reports whether a DSN targets Postgres by its URL scheme.\n")
		b.WriteString("func isPostgresDSN(dsn string) bool {\n")
		b.WriteString("\treturn strings.HasPrefix(dsn, \"postgres://\") || strings.HasPrefix(dsn, \"postgresql://\")\n")
		b.WriteString("}\n")
	}
	// gofmt the generated source so it lands gofmt-clean (import grouping etc.);
	// fall back to the unformatted string if it somehow does not parse.
	if formatted, err := format.Source([]byte(b.String())); err == nil {
		return string(formatted)
	}
	return b.String()
}

// envKey turns a composition name into an environment-variable-safe prefix
// (upper-cased, non-alphanumerics to "_").
func envKey(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 32)
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// renderGoMod renders the composed binary's go.mod: its module path, Go version, a
// require for the SDK (derived pin) + the migration sub-module + each member's
// enclosing Go module, and a `replace` for every local (--path) member so
// `go mod tidy` resolves it from disk instead of fetching an unpublished path.
func renderGoMod(modulePath, goVersion, sdkVersion string, refs []resolvedRef, pgEnabled bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "module %s\n\n", modulePath)
	fmt.Fprintf(&b, "go %s\n\n", goVersion)
	b.WriteString("require (\n")
	fmt.Fprintf(&b, "\tgithub.com/infobloxopen/devedge-sdk %s\n", sdkVersion)
	fmt.Fprintf(&b, "\t%s %s\n", gormtxModule, sdkVersion)
	if pgEnabled {
		fmt.Fprintf(&b, "\t%s %s\n", postgresDriverModule, postgresDriverVersion)
	}
	// Deduplicate by module path; the same enclosing module may host >1 member.
	seen := map[string]struct{}{}
	for _, r := range refs {
		if _, dup := seen[r.GoModulePath]; dup {
			continue
		}
		seen[r.GoModulePath] = struct{}{}
		fmt.Fprintf(&b, "\t%s %s\n", r.GoModulePath, r.RequireVersion)
	}
	b.WriteString(")\n")

	// Local members: a replace so `go mod tidy` resolves them from the checkout.
	replaces := make([]resolvedRef, 0, len(refs))
	seenRep := map[string]struct{}{}
	for _, r := range refs {
		if r.ReplaceTarget == "" {
			continue
		}
		if _, dup := seenRep[r.GoModulePath]; dup {
			continue
		}
		seenRep[r.GoModulePath] = struct{}{}
		replaces = append(replaces, r)
	}
	if len(replaces) > 0 {
		b.WriteString("\nreplace (\n")
		for _, r := range replaces {
			fmt.Fprintf(&b, "\t%s => %s\n", r.GoModulePath, r.ReplaceTarget)
		}
		b.WriteString(")\n")
	}
	return b.String()
}
