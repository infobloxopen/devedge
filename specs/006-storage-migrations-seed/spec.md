# Feature Specification: Schema migrations and dev seed on `up`

**Feature Branch**: `006-storage-migrations-seed`
**Created**: 2026-06-02
**Status**: Draft
**Input**: User description: "Run the service schema migrations and dev seed data against its provisioned Postgres dependency during de project up, so a deployed or locally-run workload starts against a ready, populated database; de project down clears the data with --clean per existing semantics."

## Context

Feature 003 provisions a **per-service isolated Postgres database + credentials** for a declared
dependency, reached over a hotload DSN; feature 005 **deploys the service workload** into the resolved
cluster and wires it to that database over in-cluster DNS. But the database devedge hands a service is
**empty** — it has no schema and no data. A deployed (or locally-run) service that connects to an empty
database cannot actually serve: its first query against a missing table fails. devedge today declares and
runs the *dependency* (003) and the *workload* (005), but not the **schema** that makes the dependency
usable.

This feature closes that gap. A service declares its **schema migrations** (and optional **dev seed
data**), and `de project up` applies them to the provisioned Postgres **before the workload serves** — in
both the local-run inner loop and the in-cluster deploy mode — so the service starts against a ready,
correctly-shaped database with no manual `psql`/migrate steps. `de project down --clean` clears the schema
and seeded data along with the dependency data (existing 003 semantics); plain `down` preserves them.

## Clarifications

### Session 2026-06-02

- Q: Who runs migrations, and how, in each mode? → A: **devedge owns the step in both modes; the app
  never self-migrates on startup.** Local-run: `de` applies migrations client-side over the connectivity
  003 already establishes, before signaling the local process. In-cluster deploy (005): a Helm
  `pre-install`/`pre-upgrade` hook Job runs migrations to completion **before** the Deployment rolls.
  Migrations follow expand/contract (backward-compatible) discipline so migrate-then-roll is safe.
- Q: Where does the in-cluster Job get the migration runner? → A: **the service's own image, invoked with
  a `migrate` subcommand/entrypoint** (migrations travel with the code and are version-matched to the
  app). The migrate engine is the **`github.com/infobloxopen/migrate`** fork (verify its exact
  capabilities/flags at plan time).
- Q: How is repeated seed kept safe (FR-005)? → A: **devedge applies dev seed once per fresh database and
  records that it ran**; re-running `up` does not re-seed; `de project down --clean` resets so the next
  `up` re-seeds. Developers write plain seed SQL — no idempotency burden on the author.
- Q: Are reverse/rollback migrations in scope? → A: **Yes.** devedge supports reversing applied
  migrations; the **down step of each applied migration is persisted to a state store (a state directory
  or the database) at apply time**, so a rollback remains possible even when the currently-deployed
  service image does not contain the corresponding down files (the `github.com/infobloxopen/migrate`
  fork's mechanism). `de project down --clean` remains the full reset.
- Q: Does CI apply dev seed? → A: **CI (`de ci run`, ephemeral cluster) applies schema migrations only,
  not dev seed.** Tests arrange their own fixtures; dev seed is a local-inner-loop convenience.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Migrations bring the database to the declared schema on `up` (Priority: P1) 🎯 MVP

A developer declares versioned schema migrations for the service's Postgres dependency, then runs
`de project up`. devedge provisions the dependency (003) and applies every not-yet-applied migration,
bringing the database to the latest declared schema version — with no manual `psql` or migrate commands.

**Why this priority**: This is the core value — a usable database. Without the schema, the provisioned
database and the deployed workload from 005 cannot do anything. Everything else builds on this.

**Independent Test**: On a clean machine, declare one migration that creates a table and run
`de project up`; the table exists in the provisioned database. Re-run `up`; the migration is not
re-applied and the schema is unchanged. Delivers a correctly-shaped database end to end.

**Acceptance Scenarios**:

1. **Given** a project that declares a Postgres dependency and one or more schema migrations, **When**
   `de project up` runs, **Then** the dependency is provisioned and the database schema is brought to the
   latest declared version before the workload serves, reported to the user.
2. **Given** a project whose schema is already at the latest version, **When** `de project up` is re-run
   with no new migrations, **Then** no migration is re-applied, the schema is unchanged, and `up` succeeds
   (idempotent).
3. **Given** a project whose declared migrations have grown since the last run, **When** `de project up`
   runs again, **Then** only the not-yet-applied migrations are applied and previously-applied ones are
   left untouched.

---

### User Story 2 - The workload starts against a ready, migrated database — both modes (Priority: P1)

The service — whether running as the local-run process or as the in-cluster deployed workload (005) —
begins serving only after migrations have succeeded, so it never runs against an unmigrated or
half-migrated schema. The behavior is the same in both modes.

**Why this priority**: This is what makes the migrated schema meaningful: it turns a deployed-but-broken
service (005 against an empty database) into a service that can actually serve its first request. A
workload that races its own migrations is the failure this story prevents.

**Independent Test**: Declare a workload that, on start, queries a migrated table; run `de project up` in
local-run mode and again with in-cluster deploy. In both, the workload's first dependent query succeeds
because the schema was applied first.

**Acceptance Scenarios**:

1. **Given** a service whose workload depends on the migrated schema, **When** `de project up` runs in
   local-run mode, **Then** migrations complete before the local process is told its database is ready,
   and the process's first dependent query succeeds.
2. **Given** the same service with in-cluster deploy (005), **When** `de project up --deploy` runs,
   **Then** migrations complete before the deployed workload serves, and the workload's first dependent
   query succeeds.
3. **Given** a service that declares no migrations, **When** `de project up` runs, **Then** behavior is
   exactly as before this feature (no schema step), in both modes.

---

### User Story 3 - Optional dev seed data populates the database (Priority: P2)

A developer optionally declares dev seed data; after migrations succeed, `de project up` populates the
database with it, so the local development experience starts with useful sample/reference data instead of
an empty-but-correct schema.

**Why this priority**: Seed data improves the inner-loop experience but is not required for a service to
function. It comes after a reliable schema.

**Independent Test**: Declare a migration plus a seed that inserts a known row; run `de project up`; the
seeded row is present. Re-run `up`; the seed does not error or duplicate the row.

**Acceptance Scenarios**:

1. **Given** a project that declares migrations and dev seed data, **When** `de project up` runs, **Then**
   the seed is applied after migrations succeed and the seeded data is present.
2. **Given** a project whose seed has already been applied, **When** `de project up` is re-run, **Then**
   the seed step is safe to repeat — it neither fails nor duplicates seeded data.
3. **Given** a project that declares migrations but no seed, **When** `de project up` runs, **Then** the
   schema is applied and no seed step runs.

---

### Edge Cases

- **Migration failure**: a migration that errors aborts `up` with an actionable message, the workload is
  not started/served against the partial schema, and a corrected re-run recovers without manual database
  cleanup for the common case.
- **Dependency not yet ready**: migration application waits for the provisioned dependency to become
  reachable (bounded retry, consistent with 003 readiness) rather than failing immediately.
- **`down` then `up`**: `de project down` without `--clean` preserves the schema and data; a subsequent
  `up` re-applies nothing (idempotent) and the workload sees the prior data.
- **`down --clean` then `up`**: `--clean` clears the schema and seeded data; the next `up` rebuilds both
  from scratch.
- **Rollback across image versions**: reversing the schema to the version an older image expects uses the
  down steps persisted at apply time, so the reversal works even though the older image does not ship those
  down migrations.
- **Co-existence**: two co-located services on the shared cluster, each with its own isolated database
  (003), may declare identical table names without colliding; clearing one with `--clean` leaves the
  other intact.
- **Non-relational dependency present**: when both Postgres and a non-relational dependency (e.g. Redis)
  are declared, migrations and seed apply only to Postgres; the non-relational dependency is untouched.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: A service MUST be able to declare **versioned schema migrations** for its relational
  (Postgres) dependency in its configuration, additive to the existing `kind: Service` / dependencies
  schema.
- **FR-002**: A service MUST be able to **optionally** declare **dev seed data** for that dependency.
- **FR-003**: `de project up` MUST apply all not-yet-applied declared migrations to the provisioned
  Postgres dependency, bringing its schema to the latest declared version, **before** the service workload
  (local-run process or in-cluster deployment) is allowed to serve.
- **FR-004**: Migration application MUST be **idempotent** across repeated `up` runs: already-applied
  migrations MUST NOT be re-applied, and re-running `up` with no new migrations MUST make no schema change.
- **FR-005**: When dev seed data is declared, devedge MUST apply it **after** migrations succeed and MUST
  **record that it has run**, so that re-running `up` does NOT re-apply it (no failure, no duplicate data);
  `de project down --clean` MUST reset this record so the next `up` re-seeds. Developers are NOT required
  to make seed scripts idempotent.
- **FR-006**: Migrations and seed MUST apply equally for both the **local-run** inner loop and the
  **in-cluster deploy** mode (005), so a service behaves the same against its database in either mode.
- **FR-007**: When a migration fails, `de project up` MUST stop with an **actionable** error, MUST NOT
  start or serve the workload against a partially-migrated database, and MUST leave the database in a state
  a corrected re-run can recover from without manual cleanup for the common case.
- **FR-008**: `de project down --clean` MUST clear the migrated schema and seeded data together with the
  dependency data (existing 003 `--clean` semantics); `de project down` without `--clean` MUST preserve
  the schema and data.
- **FR-009**: Migrations and seed MUST operate only within each service's existing per-service isolated
  database (003), so applying or clearing one service's schema/data MUST NOT affect another co-located
  service on the shared cluster.
- **FR-010**: devedge MUST report the migration outcome (e.g. the version reached or number of migrations
  applied, or that the schema was already current) and the seed outcome, so database state is observable.
- **FR-011**: Migrations and seed MUST apply only to the relational (Postgres) dependency; non-relational
  dependencies (e.g. Redis) are out of scope for this feature and MUST be left unaffected.
- **FR-012**: devedge MUST support **reversing applied migrations** (rollback). The down step for each
  applied migration MUST be **persisted to a state store** (a state directory or the database) at apply
  time, so a rollback remains possible even when the currently-deployed service image does not contain the
  corresponding down migrations.
- **FR-013**: Dev seed MUST be treated as a local-development convenience: it MUST be applied for the local
  inner loop (and the dev cluster), and MUST NOT be applied during CI runs (`de ci run`), which apply
  schema migrations only.

### Key Entities

- **Schema migration set**: the ordered, versioned set of schema changes a service declares for its
  relational dependency; defines the target schema state. Tracked so already-applied versions are skipped
  on re-run.
- **Dev seed data**: optional sample/reference data a service declares to populate its database after
  migration, for the local development experience; applied once per fresh database and tracked by devedge
  so re-running `up` does not re-apply it (FR-005).
- **Database readiness**: the condition (schema at the declared version, seed applied when declared) the
  dependency must reach before the workload is allowed to serve — distinct from the dependency merely
  being provisioned/up (003).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: After `de project up`, a service declaring N migrations finds its database at the declared
  schema (all declared changes present) in 100% of clean-start runs, with no manual database steps.
- **SC-002**: Re-running `de project up` with no new migrations makes zero schema changes and completes
  without error (idempotent) in 100% of runs.
- **SC-003**: A workload that issues a query depending on the migrated schema succeeds on its first request
  after `up`, in both local-run and in-cluster deploy modes.
- **SC-004**: When a migration fails, `up` reports an actionable error and the workload does not serve
  against the partial schema in 100% of failure cases; a corrected re-run then succeeds without manual
  cleanup.
- **SC-005**: `de project down --clean` removes the schema and seeded data, and a subsequent `up` rebuilds
  them — verified by the data being absent after `--clean` and present again after `up`.
- **SC-006**: For two co-located services on the shared cluster, migrations and seed operate only on each
  service's own database; clearing one with `--clean` leaves the other intact in 100% of cases.
- **SC-007**: An applied migration can be reversed via devedge, and the reversal succeeds **even when the
  running service image does not contain the down migrations**, in 100% of cases where the down step was
  persisted at apply time.

## Assumptions

- The dependency runtime (003), resolved-cluster model (004), and workload deploy (005) are prerequisites
  and unchanged; this feature adds the schema layer on top of the database 003 already provisions and 005
  already connects to.
- **Migration format & engine**: a service authors versioned SQL migration files in a conventional
  per-service directory (forward/up plus reversible/down). The migrate engine is the
  `github.com/infobloxopen/migrate` fork (see Clarifications); its exact capabilities, flags, and
  state-store layout for persisted down migrations MUST be verified against the fork at plan time before
  building.
- **Where schema applies**: schema migrations run wherever `de project up` provisions the dependency — the
  shared dev cluster and CI's ephemeral cluster (004) alike (FR-013). Dev seed is local-inner-loop only and
  is not applied in CI (FR-013).
- **Ordering & mechanism**: migrations run after the dependency is provisioned and reachable, and before
  the workload serves, in both modes — local-run via `de` client-side, in-cluster via a Helm
  `pre-install`/`pre-upgrade` hook Job before the Deployment rolls (see Clarifications). The observable
  requirement (FR-003/FR-006) is mode-agnostic; the mechanism is the resolved plan direction.
- Per-service isolation, co-existence, and `--clean` semantics follow 003/004 conventions
  (project/service-slug naming, per-service database and credentials).
- A service that declares no migrations and no seed sees no behavior change from this feature.
