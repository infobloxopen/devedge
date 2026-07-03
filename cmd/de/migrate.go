package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/infobloxopen/devedge/internal/dsn"
	"github.com/infobloxopen/devedge/internal/migrate"
	"github.com/infobloxopen/devedge/pkg/config"
)

// migrateCmd is `de migrate`, the schema-migration authoring + apply surface
// (WS-022). It drives the org-standard infobloxopen/migrate fork through
// internal/migrate against sequentially-numbered NNNN_<name>.up/down.sql files:
//
//	de migrate new <name>   scaffold the next-numbered up/down pair
//	de migrate lint         sequence/pairing gate (+ best-effort squawk)
//	de migrate up           apply to the target version (connection-level timeouts)
//	de migrate status       current applied version vs on-disk target
//	de migrate verify       assert at target + not dirty (the unattended gate)
//
// Migrations are sequentially numbered (never timestamped) so a duplicate-number
// merge conflict is the signal that two changes race on the schema. lock_timeout /
// statement_timeout are set on the migration CONNECTION (never per file), so a
// contended migration fails fast instead of stalling the database.
func migrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Author and apply sequentially-numbered SQL schema migrations",
		Long: `Author and apply sequentially-numbered SQL schema migrations.

Migrations are golang-migrate-style files applied by the org-standard
infobloxopen/migrate fork: NNNN_<name>.up.sql + NNNN_<name>.down.sql, 4-digit
zero-padded, no gaps, no timestamps. Each change ships its inverse 'down'.

Near-zero-downtime rules (enforced/encoded here):
  - lock_timeout / statement_timeout live on the migration CONNECTION, never in
    a file (a per-file SET line breaks CREATE INDEX CONCURRENTLY).
  - CREATE/DROP INDEX CONCURRENTLY must be ALONE in its own file.
  - 'de migrate lint' fails on a numbering gap, a missing/duplicate pair, or a
    CONCURRENTLY statement mixed with others; 'squawk' adds deep Postgres checks
    when it is on PATH.`,
	}
	cmd.AddCommand(
		migrateNewCmd(),
		migrateLintCmd(),
		migrateUpCmd(),
		migrateStatusCmd(),
		migrateVerifyCmd(),
	)
	// A migrate failure (lint violations, dirty schema, ...) is already an
	// actionable message; don't bury it under a usage dump.
	for _, sub := range cmd.Commands() {
		sub.SilenceUsage = true
	}
	return cmd
}

// migrateOpts is the shared flag set for the migrate subcommands.
type migrateOpts struct {
	file       string        // devedge.yaml path
	dir        string        // migrations dir override
	dsn        string        // explicit DSN override
	dependency string        // which postgres dependency to target (when several)
	downStore  string        // persisted down-store dir override
	lockTO     time.Duration // lock_timeout on the migration connection
	stmtTO     time.Duration // statement_timeout on the migration connection
}

// bindCommonFlags binds the flags shared by every subcommand that reads a
// migrations dir and/or a project file.
func bindCommonFlags(cmd *cobra.Command, o *migrateOpts) {
	cmd.Flags().StringVarP(&o.file, "file", "f", "devedge.yaml", "project config file (to resolve the migrations dir + DSN)")
	cmd.Flags().StringVar(&o.dir, "dir", "", "migrations directory (default: the devedge.yaml dependency's, else ./module/migrations for an SDK service scaffold, else ./db/migrations)")
	cmd.Flags().StringVar(&o.dependency, "dependency", "", "which postgres dependency to target when the project declares several")
}

// bindConnFlags binds the flags shared by the DB-touching subcommands.
func bindConnFlags(cmd *cobra.Command, o *migrateOpts) {
	cmd.Flags().StringVar(&o.dsn, "dsn", "", "postgres DSN to apply against (default: DATABASE_URL, else the DSN de project up wrote)")
	cmd.Flags().DurationVar(&o.lockTO, "lock-timeout", 2*time.Second, "lock_timeout set on the migration connection (0 disables)")
	cmd.Flags().DurationVar(&o.stmtTO, "statement-timeout", 60*time.Second, "statement_timeout set on the migration connection (0 disables)")
}

// ---- de migrate new ---------------------------------------------------------

func migrateNewCmd() *cobra.Command {
	var o migrateOpts
	cmd := &cobra.Command{
		Use:   "new NAME",
		Short: "Scaffold the next-numbered up/down migration pair",
		Long: `Scaffold NNNN_<name>.up.sql and NNNN_<name>.down.sql at the next
sequential number (4-digit zero-padded) in the project's migrations directory.

The files carry a header comment documenting the authoring rules. Timeouts are
NOT written into the file — they belong on the connection (de sets them).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateNew(cmd, &o, args[0])
		},
	}
	bindCommonFlags(cmd, &o)
	return cmd
}

// nameSanitizeRE collapses any run of characters outside [a-z0-9] into a single
// underscore, so a human title becomes a safe migration slug.
var nameSanitizeRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugifyMigrationName(name string) string {
	s := nameSanitizeRE.ReplaceAllString(strings.ToLower(name), "_")
	return strings.Trim(s, "_")
}

func runMigrateNew(cmd *cobra.Command, o *migrateOpts, rawName string) error {
	out := cmd.OutOrStdout()
	name := slugifyMigrationName(rawName)
	if name == "" {
		return fmt.Errorf("migration name %q has no usable characters (use letters/digits, e.g. add_email_index)", rawName)
	}

	dir, err := resolveMigrationsDir(o, false)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create migrations dir %s: %w", dir, err)
	}

	// An SDK service reserves 0001 for the framework baseline, so its first
	// domain migration is 0002; a raw service starts at 0001.
	firstExpected := uint(1)
	if migrate.FrameworkBaselinePresent(dir) {
		firstExpected = 2
	}
	next, err := nextVersion(dir, firstExpected)
	if err != nil {
		return err
	}
	num := fmt.Sprintf("%04d", next)
	upName := fmt.Sprintf("%s_%s.up.sql", num, name)
	downName := fmt.Sprintf("%s_%s.down.sql", num, name)
	upPath := filepath.Join(dir, upName)
	downPath := filepath.Join(dir, downName)

	for _, p := range []string{upPath, downPath} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("refusing to overwrite existing %s", p)
		}
	}
	if err := os.WriteFile(upPath, []byte(migrationHeader(upName, downName, "up")), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", upPath, err)
	}
	if err := os.WriteFile(downPath, []byte(migrationHeader(downName, upName, "down")), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", downPath, err)
	}

	fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("created"), upPath)
	fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("created"), downPath)
	fmt.Fprintf(out, "%s edit the pair, then run %s\n", colorLabel.Sprint("next:"), colorHost.Sprint("de migrate lint"))
	return nil
}

// migrationHeader returns the header comment for a new migration file. It never
// emits a `SET lock_timeout` line — timeouts live on the connection, and a SET
// line would be a second statement that breaks CONCURRENTLY DDL.
func migrationHeader(self, inverse, dir string) string {
	verb := "forward"
	if dir == "down" {
		verb = "inverse (rollback)"
	}
	return fmt.Sprintf(`-- %s
--
-- devedge migration authoring rules:
--   * Sequential numbering (NNNN_<name>, 4-digit, no gaps, no timestamps). If a
--     PR collides on a number, renumber yours — the conflict is the signal.
--   * One logical change per migration; ship the inverse in %s.
--   * Near-zero-downtime: avoid AccessExclusiveLock on hot tables. Prefer
--     ADD COLUMN (no volatile default); CREATE INDEX CONCURRENTLY (alone in its
--     own file); ADD CONSTRAINT ... NOT VALID then a later VALIDATE CONSTRAINT.
--     Never a bare ALTER COLUMN TYPE or RENAME on a live table.
--   * Make it idempotent: IF NOT EXISTS / IF EXISTS; guard ADD CONSTRAINT with a
--     DO $$ BEGIN ... EXCEPTION WHEN duplicate_object THEN NULL; END $$; block.
--   * Do NOT set lock_timeout/statement_timeout here — de sets them on the
--     connection. A SET line would break CREATE INDEX CONCURRENTLY.
--   * Lint before commit: de migrate lint
--
-- Write the %s migration below.
`, self, inverse, verb)
}

// nextVersion returns the next migration version to scaffold: max(on-disk)+1, or
// firstExpected for an empty/missing dir (1 for a raw service, 2 for an SDK
// service whose 0001 is the reserved framework baseline).
func nextVersion(dir string, firstExpected uint) (uint, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		return 0, fmt.Errorf("scan migrations %s: %w", dir, err)
	}
	var max uint
	for _, f := range matches {
		if v, ok := parseMigrationVersion(filepath.Base(f)); ok && v > max {
			max = v
		}
	}
	if max == 0 {
		return firstExpected, nil
	}
	return max + 1, nil
}

// ---- de migrate lint --------------------------------------------------------

func migrateLintCmd() *cobra.Command {
	var o migrateOpts
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Check migration numbering/pairing (+ best-effort squawk)",
		Long: `Two checks:

  (a) A pure-Go sequence/pairing gate (always runs, authoritative): file names
      match NNNN_<name>.(up|down).sql, no numbering gaps, no duplicate numbers,
      every up has a matching down, and no CONCURRENTLY statement is mixed with
      others in the same file.

  (b) A best-effort 'squawk' run over *.up.sql for deep Postgres safety checks
      (blocking CREATE INDEX, ADD CONSTRAINT without NOT VALID, unsafe type
      changes, ...). squawk is a separate tool; if it is not on PATH the deep
      check is skipped (never a failure) and an install hint is printed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateLint(cmd, &o)
		},
	}
	bindCommonFlags(cmd, &o)
	return cmd
}

func runMigrateLint(cmd *cobra.Command, o *migrateOpts) error {
	out := cmd.OutOrStdout()
	dir, err := resolveMigrationsDir(o, true)
	if err != nil {
		return err
	}

	// An SDK service reserves 0001 for the framework baseline (composed by the
	// applier), so its first on-disk (domain) migration must be 0002; a raw
	// service owns 0001 itself.
	firstExpected := uint(1)
	if migrate.FrameworkBaselinePresent(dir) {
		firstExpected = 2
	}
	problems, warnings := lintSequence(dir, firstExpected)
	for _, w := range warnings {
		fmt.Fprintf(out, "%s %s\n", colorWarning.Sprint("warning:"), w)
	}
	for _, p := range problems {
		fmt.Fprintf(out, "%s %s\n", colorError.Sprint("lint:"), p)
	}

	squawkFailed := runSquawk(cmd, dir)

	if len(problems) > 0 || squawkFailed {
		n := len(problems)
		if squawkFailed {
			n++
		}
		return fmt.Errorf("migration lint failed (%d problem(s) in %s)", n, dir)
	}
	fmt.Fprintf(out, "%s migrations in %s are well-formed\n", colorSuccess.Sprint("ok:"), dir)
	return nil
}

// migrationFileRE matches a golang-migrate file name: <version>_<name>.(up|down).sql.
var migrationFileRE = regexp.MustCompile(`^([0-9]+)_(.+)\.(up|down)\.sql$`)

// parseMigrationVersion extracts the leading decimal version from a migration file
// base name, e.g. "0002_add_email.up.sql" -> 2.
func parseMigrationVersion(base string) (uint, bool) {
	m := migrationFileRE.FindStringSubmatch(base)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return uint(v), true
}

// lintSequence runs the authoritative pure-Go checks over dir and returns the
// (fatal) problems and (non-fatal) warnings it found. firstExpected is the
// version the first on-disk migration must carry: 1 for a raw service, 2 for an
// SDK service (whose 0001 is the reserved framework baseline).
func lintSequence(dir string, firstExpected uint) (problems, warnings []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{fmt.Sprintf("read migrations dir %s: %v", dir, err)}, nil
	}

	type titleDir struct {
		title string
		file  string
	}
	ups := map[uint]titleDir{}
	downs := map[uint]titleDir{}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue // allow non-SQL files (README, .gitkeep) to coexist
		}
		m := migrationFileRE.FindStringSubmatch(name)
		if m == nil {
			problems = append(problems, fmt.Sprintf("unrecognized migration file %q (expected NNNN_<name>.up.sql / .down.sql)", name))
			continue
		}
		v, _ := parseMigrationVersion(name)
		title, direction := m[2], m[3]
		target := ups
		if direction == "down" {
			target = downs
		}
		if prev, dup := target[v]; dup {
			problems = append(problems, fmt.Sprintf("duplicate %s migration for version %04d: %q and %q", direction, v, prev.file, name))
			continue
		}
		target[v] = titleDir{title: title, file: name}

		// CONCURRENTLY must be alone in its file (golang-migrate runs a file in one
		// implicit transaction; CONCURRENTLY DDL cannot run inside a transaction).
		if content, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			if hasConcurrently(string(content)) && countStatements(string(content)) > 1 {
				problems = append(problems, fmt.Sprintf("%s: CREATE/DROP INDEX CONCURRENTLY must be the ONLY statement in its file (it cannot run in a transaction; golang-migrate runs a file as one)", name))
			}
		}
	}

	// Pairing: every up needs a down (and vice versa); warn on mismatched titles.
	for v, up := range ups {
		down, ok := downs[v]
		if !ok {
			problems = append(problems, fmt.Sprintf("migration %04d (%s) has no matching .down.sql — ship the inverse", v, up.title))
			continue
		}
		if down.title != up.title {
			warnings = append(warnings, fmt.Sprintf("version %04d up/down titles differ (%q vs %q)", v, up.title, down.title))
		}
	}
	for v, down := range downs {
		if _, ok := ups[v]; !ok {
			problems = append(problems, fmt.Sprintf("orphan down migration %04d (%s) has no matching .up.sql", v, down.title))
		}
	}

	// Contiguity: sorted up versions must be firstExpected, +1, +2, ... with no
	// gaps. All arithmetic below uses only addition/comparison on the ascending,
	// de-duplicated slice (each version keys a map, so versions strictly
	// increase) — never an unsigned subtraction that could underflow on a
	// version 0 or an out-of-range first version.
	versions := make([]uint, 0, len(ups))
	for v := range ups {
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	if len(versions) > 0 {
		if versions[0] != firstExpected {
			if firstExpected == 2 {
				problems = append(problems, fmt.Sprintf("first migration must be 0002 (0001 is reserved for the SDK framework baseline), found %04d", versions[0]))
			} else {
				problems = append(problems, fmt.Sprintf("first migration must be %04d, found %04d", firstExpected, versions[0]))
			}
		}
		for i := 1; i < len(versions); i++ {
			// versions[i] > versions[i-1] (sorted + unique), so versions[i-1]+1
			// never overflows past versions[i]; the "missing" is the next number.
			if versions[i] != versions[i-1]+1 {
				problems = append(problems, fmt.Sprintf("gap in migration sequence: %04d follows %04d (missing %04d)", versions[i], versions[i-1], versions[i-1]+1))
			}
		}
	}
	return problems, warnings
}

// sqlCommentRE strips `-- line comments` and `/* block comments */` so statement
// counting and keyword detection ignore commented-out SQL.
var (
	lineCommentRE  = regexp.MustCompile(`--[^\n]*`)
	blockCommentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

func stripSQLComments(sql string) string {
	sql = blockCommentRE.ReplaceAllString(sql, " ")
	sql = lineCommentRE.ReplaceAllString(sql, " ")
	return sql
}

// hasConcurrently reports whether sql contains a CONCURRENTLY keyword outside of
// comments (case-insensitive).
func hasConcurrently(sql string) bool {
	return strings.Contains(strings.ToUpper(stripSQLComments(sql)), "CONCURRENTLY")
}

// countStatements counts `;`-separated non-empty statements after stripping
// comments. A heuristic (it does not fully parse dollar-quoted bodies), used only
// to flag a CONCURRENTLY statement sharing a file with others.
func countStatements(sql string) int {
	n := 0
	for _, part := range strings.Split(stripSQLComments(sql), ";") {
		if strings.TrimSpace(part) != "" {
			n++
		}
	}
	return n
}

// runSquawk invokes squawk over the dir's *.up.sql files if squawk is on PATH.
// Absence is never a failure (an install hint is printed); squawk reporting
// violations (non-zero exit) fails the lint. Returns true when squawk failed.
func runSquawk(cmd *cobra.Command, dir string) bool {
	out := cmd.OutOrStdout()
	bin, err := exec.LookPath("squawk")
	if err != nil {
		fmt.Fprintf(out, "%s squawk not on PATH — skipping deep Postgres lint. Install: `npm i -g squawk-cli` or `brew install squawk` (https://squawkhq.com)\n", colorWarning.Sprint("note:"))
		return false
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if len(files) == 0 {
		return false
	}
	sq := exec.Command(bin, files...)
	output, runErr := sq.CombinedOutput()
	if len(output) > 0 {
		fmt.Fprint(out, string(output))
		if !strings.HasSuffix(string(output), "\n") {
			fmt.Fprintln(out)
		}
	}
	if runErr != nil {
		fmt.Fprintf(out, "%s squawk reported migration-safety issues\n", colorError.Sprint("lint:"))
		return true
	}
	fmt.Fprintf(out, "%s squawk found no issues\n", colorLabel.Sprint("squawk:"))
	return false
}

// ---- de migrate up / status / verify ---------------------------------------

func migrateUpCmd() *cobra.Command {
	var o migrateOpts
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Apply pending migrations to the on-disk target version",
		Long: `Apply migrations through the infobloxopen/migrate fork up (or down) to
the highest version on disk. lock_timeout and statement_timeout are set on the
migration CONNECTION so a contended migration fails fast. Idempotent; recovers a
dirty database left by a prior failed run.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateUp(cmd, &o)
		},
	}
	bindCommonFlags(cmd, &o)
	bindConnFlags(cmd, &o)
	cmd.Flags().StringVar(&o.downStore, "downstore", "", "persisted down-store dir (default: the project's, else <migrations>/../.devedge-downstore)")
	return cmd
}

func runMigrateUp(cmd *cobra.Command, o *migrateOpts) error {
	out := cmd.OutOrStdout()
	dir, err := resolveMigrationsDir(o, true)
	if err != nil {
		return err
	}
	rawDSN, err := resolveDSN(o)
	if err != nil {
		return err
	}
	// Wire the timeouts onto the migration connection (the near-zero-downtime
	// seatbelt) — never into a migration file.
	connDSN, err := migrate.WithConnTimeouts(rawDSN, o.lockTO, o.stmtTO)
	if err != nil {
		return err
	}
	store := migrate.DownStore{Dir: resolveDownStore(o, dir)}
	if err := os.MkdirAll(store.Dir, 0o700); err != nil {
		return fmt.Errorf("prepare down-store %s: %w", store.Dir, err)
	}

	// Compose the SDK framework baseline (0001) ahead of the on-disk module
	// migrations (0002+) when this service depends on the SDK migration engine,
	// so `de migrate up` applies the SAME version space the service applies at
	// Serve (1=framework, 2+=domain). A raw/non-SDK dep composes nothing.
	src, cleanup, composed, err := migrate.ComposeSource(dir)
	if err != nil {
		return err
	}
	defer cleanup()

	fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("migrations:"), dir)
	if composed {
		fmt.Fprintf(out, "%s composing SDK framework baseline (0001) ahead of module migrations (0002+)\n", colorLabel.Sprint("baseline:"))
	}
	fmt.Fprintf(out, "%s lock_timeout=%s statement_timeout=%s\n", colorLabel.Sprint("connection:"), o.lockTO, o.stmtTO)

	res, err := migrate.NewForkApplier().Migrate(context.Background(), connDSN, src, store)
	if err != nil {
		return err
	}
	switch {
	case res.AlreadyCurrent:
		fmt.Fprintf(out, "%s already current (v%d)\n", colorSuccess.Sprint("ok:"), res.ToVersion)
	case res.ToVersion < res.FromVersion:
		fmt.Fprintf(out, "%s rolled back (v%d → v%d)\n", colorSuccess.Sprint("ok:"), res.FromVersion, res.ToVersion)
	default:
		fmt.Fprintf(out, "%s applied %d migration(s) (v%d → v%d)\n", colorSuccess.Sprint("ok:"), res.Applied, res.FromVersion, res.ToVersion)
	}
	return nil
}

func migrateStatusCmd() *cobra.Command {
	var o migrateOpts
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report current applied version vs on-disk target (+ dirty state)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateStatus(cmd, &o)
		},
	}
	bindCommonFlags(cmd, &o)
	bindConnFlags(cmd, &o)
	return cmd
}

func runMigrateStatus(cmd *cobra.Command, o *migrateOpts) error {
	out := cmd.OutOrStdout()
	dir, err := resolveMigrationsDir(o, true)
	if err != nil {
		return err
	}
	rawDSN, err := resolveDSN(o)
	if err != nil {
		return err
	}
	// Status must measure against the SAME composed set `up` applies, so an SDK
	// service's framework baseline (0001) counts toward the on-disk target.
	src, cleanup, _, err := migrate.ComposeSource(dir)
	if err != nil {
		return err
	}
	defer cleanup()
	st, err := migrate.NewForkApplier().Status(context.Background(), rawDSN, src)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s %s\n", colorLabel.Sprint("migrations:"), dir)
	fmt.Fprintf(out, "current version: %d\n", st.CurrentVersion)
	fmt.Fprintf(out, "target version:  %d\n", st.TargetVersion)
	if st.Dirty {
		fmt.Fprintf(out, "dirty:           %s (a prior run failed part-way; `de migrate up` recovers it)\n", colorWarning.Sprint("yes"))
	} else {
		fmt.Fprintf(out, "dirty:           no\n")
	}
	switch {
	case st.UpToDate():
		fmt.Fprintf(out, "%s up to date\n", colorSuccess.Sprint("ok:"))
	case st.Dirty:
		// The dirty line above already told the story; nothing to add.
	case st.CurrentVersion > st.TargetVersion:
		// Deploying an older source rolls the schema back; report it without a
		// uint underflow (TargetVersion-CurrentVersion would wrap otherwise).
		fmt.Fprintf(out, "%s ahead of target by %d version(s) (`de migrate up` rolls back to v%d)\n",
			colorWarning.Sprint("pending:"), st.CurrentVersion-st.TargetVersion, st.TargetVersion)
	default:
		fmt.Fprintf(out, "%s %d migration(s) pending\n", colorWarning.Sprint("pending:"), st.TargetVersion-st.CurrentVersion)
	}
	return nil
}

func migrateVerifyCmd() *cobra.Command {
	var o migrateOpts
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Assert the schema is at the on-disk target and not dirty",
		Long: `Post-apply assertion (the unattended Job's final gate): the database is
at the highest on-disk migration version and not in a dirty state. Exits non-zero
with an actionable message otherwise.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrateVerify(cmd, &o)
		},
	}
	bindCommonFlags(cmd, &o)
	bindConnFlags(cmd, &o)
	return cmd
}

func runMigrateVerify(cmd *cobra.Command, o *migrateOpts) error {
	out := cmd.OutOrStdout()
	dir, err := resolveMigrationsDir(o, true)
	if err != nil {
		return err
	}
	rawDSN, err := resolveDSN(o)
	if err != nil {
		return err
	}
	// Verify against the SAME composed set `up` applies (framework baseline
	// included for an SDK service).
	src, cleanup, _, err := migrate.ComposeSource(dir)
	if err != nil {
		return err
	}
	defer cleanup()
	st, err := migrate.NewForkApplier().Status(context.Background(), rawDSN, src)
	if err != nil {
		return err
	}
	if st.Dirty {
		return fmt.Errorf("schema is dirty (last migration failed part-way at v%d); run `de migrate up` to recover", st.CurrentVersion)
	}
	if st.CurrentVersion > st.TargetVersion {
		return fmt.Errorf("schema at v%d is ahead of the on-disk target v%d (%d version(s) to roll back); run `de migrate up`",
			st.CurrentVersion, st.TargetVersion, st.CurrentVersion-st.TargetVersion)
	}
	if st.CurrentVersion != st.TargetVersion {
		return fmt.Errorf("schema at v%d but on-disk target is v%d (%d migration(s) unapplied); run `de migrate up`",
			st.CurrentVersion, st.TargetVersion, st.TargetVersion-st.CurrentVersion)
	}
	fmt.Fprintf(out, "%s schema verified at v%d (not dirty)\n", colorSuccess.Sprint("ok:"), st.CurrentVersion)
	return nil
}

// ---- resolution helpers -----------------------------------------------------

// projInfo is the migration-relevant slice of a devedge.yaml, resolved once.
type projInfo struct {
	project string // metadata.name
	depName string // chosen postgres dependency with migrations
	migRel  string // that dependency's project-relative migrations path
	found   bool   // a matching postgres dependency with migrations was found
}

// loadProjInfo reads o.file (if present) and picks the postgres dependency that
// declares migrations — the one named by --dependency, or the sole one. A missing
// or non-Service file yields an empty projInfo (callers fall back to flags).
func loadProjInfo(o *migrateOpts) projInfo {
	if _, err := os.Stat(o.file); err != nil {
		return projInfo{}
	}
	res, err := config.LoadResource(o.file)
	if err != nil {
		return projInfo{}
	}
	info := projInfo{project: res.Project()}
	dd, ok := res.(config.DependencyDeclarer)
	if !ok {
		return info
	}
	for _, d := range dd.Dependencies() {
		if d.Engine != "postgres" || d.Migrations == "" {
			continue
		}
		if o.dependency != "" && d.Name != o.dependency {
			continue
		}
		info.depName = d.Name
		info.migRel = d.Migrations
		info.found = true
		break
	}
	return info
}

// isDir reports whether path exists and is a directory.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// resolveMigrationsDir determines the migrations directory. Precedence: --dir,
// then the devedge.yaml dependency's migrations path, then a `devedge-sdk new
// service` scaffold's module/migrations, then ./db/migrations. When mustExist,
// the directory must exist and be a directory.
func resolveMigrationsDir(o *migrateOpts, mustExist bool) (string, error) {
	dir := o.dir
	if dir == "" {
		switch info := loadProjInfo(o); {
		case info.found:
			dir = filepath.Join(filepath.Dir(o.file), info.migRel)
		case isDir("module/migrations"):
			// A `devedge-sdk new service` scaffold has no devedge.yaml; its
			// migrations live in module/migrations and are embedded via the
			// module's `//go:embed migrations`. Prefer that over the
			// `de project init` default so `de migrate` writes where the service
			// actually reads (finding 060).
			dir = "module/migrations"
		default:
			dir = "db/migrations"
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if mustExist {
		fi, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("migrations dir %s: %w (create one with `de migrate new <name>`)", abs, err)
		}
		if !fi.IsDir() {
			return "", fmt.Errorf("migrations path %s is not a directory", abs)
		}
	}
	return abs, nil
}

// resolveDSN determines the postgres DSN. Precedence: --dsn, DATABASE_URL, then
// the DSN file `de project up` wrote for the project's dependency. The indirect
// fsnotify:// form (devedge's env-var convention) is dereferenced to its file.
func resolveDSN(o *migrateOpts) (string, error) {
	if o.dsn != "" {
		return dereferenceDSN(o.dsn)
	}
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return dereferenceDSN(v)
	}
	info := loadProjInfo(o)
	if info.found {
		p := dsn.FilePath(devedgeHome(), info.project, info.depName)
		b, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("no DSN for dependency %q at %s — run `de project up` first, or pass --dsn", info.depName, p)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return "", fmt.Errorf("no database DSN: pass --dsn, set DATABASE_URL, or run inside a project (devedge.yaml) after `de project up`")
}

// dereferenceDSN resolves devedge's indirect DSN convention: a value of the form
// fsnotify://<engine>/<path> means the real DSN lives in that file; anything else
// is returned as-is.
func dereferenceDSN(raw string) (string, error) {
	rest, ok := strings.CutPrefix(raw, "fsnotify://")
	if !ok {
		return raw, nil
	}
	_, path, found := strings.Cut(rest, "/")
	if !found {
		return "", fmt.Errorf("malformed indirect DSN %q", raw)
	}
	b, err := os.ReadFile("/" + path)
	if err != nil {
		return "", fmt.Errorf("read DSN file: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// resolveDownStore determines the persisted down-store dir. Precedence:
// --downstore, then the project's canonical store, then a sibling of the
// migrations dir.
func resolveDownStore(o *migrateOpts, migDir string) string {
	if o.downStore != "" {
		return o.downStore
	}
	if info := loadProjInfo(o); info.found {
		return dsn.DownStoreDir(devedgeHome(), info.project, info.depName)
	}
	return filepath.Join(filepath.Dir(migDir), ".devedge-downstore")
}

// devedgeHome is the base directory for devedge state ($DEVEDGE_HOME, else
// ~/.devedge), matching the daemon's convention so `de migrate` reads the same
// DSN files `de project up` wrote.
func devedgeHome() string {
	if d := os.Getenv("DEVEDGE_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".devedge")
}
