# Phase 0 Research: Schema migrations and dev seed on `up`

Resolves the unknowns flagged in the spec's Clarifications/Assumptions and the deferred items from
`/speckit.clarify`. Each entry: **Decision · Rationale · Alternatives considered**. Code references are to
the devedge tree mapped for this plan.

## R1 — Migration engine

**Decision**: Use **`github.com/infobloxopen/migrate`** (branch `ib`), the Infoblox fork of
`golang-migrate/migrate`, as both a Go **library** (local-run apply, embedded in `de`/daemon) and the
**`migrate` subcommand** baked into a deployed service image. Postgres via the `pgx/v5` database driver;
file/`io/fs` source driver.

**Rationale**: It is the engine named in the clarification, is a maintained fork (default branch `ib`,
pushed 2026-04-14, 67 commits ahead of upstream, CVE bumps + OTel current), keeps the standard
golang-migrate surface (`Up`/`Down`/`Steps`, `schema_migrations` version table, `NNN_name.up.sql` /
`.down.sql` files), and — critically — carries the dirty-state/down-persistence delta this feature needs
(R2). devedge currently vendors **no** migration library (`go.mod` has only cobra/yaml/dns/color), so this
is a clean new `require`.

**Alternatives**: upstream `golang-migrate/migrate` (lacks the down-persistence/dirty-recovery delta —
fails FR-012/FR-007 as specified); `pressly/goose`, `ariga/atlas` (not the org standard; would diverge
from the Infoblox toolchain the framework lineage targets). Rejected.

**To confirm at implementation time**: the exact public API to enable the persisted-down-state behavior
(see R2) — the option/constructor name on `migrate.Migrate`, and whether the store is a directory or the
database. The mechanism is confirmed; the surface must be read off the `ib` source.

## R2 — Persisted down migrations + dirty-state recovery (FR-012, FR-007, SC-007)

**Decision**: Rely on the fork's **`dirtyStateConf`** mechanism (fork PR #57, "Add dirty state handling to
Down(), Steps(), and Up()", patching `migrate.go`). When enabled, `Up()`/`Down()`/`Steps()`:
- `copyFiles()` — **persist the migration files (up *and* down) to a state store** before running, so the
  down step of any applied migration survives independent of whatever image's source tree is present;
- on success, `cleanupFiles(finalVersion)` (or `cleanupFiles(0)` after a full down) trims the store to the
  live version;
- `handleDirtyState()` — if the DB is left **dirty** by a prior failed run, recover automatically instead
  of returning `ErrDirty{version}` (the upstream behavior, still the default when the config is absent).

**Rationale**: This is precisely the spec's rollback invariant ("a rollback remains possible even when the
currently-deployed service image does not contain the corresponding down migrations") *and* the FR-007
recovery promise ("leave the database in a state a corrected re-run can recover from without manual
cleanup") — both delivered by one library feature rather than re-implemented in devedge. Backward
compatible: without the config the engine behaves like stock golang-migrate.

**Store location**:
- *Local-run* — a host state directory under the devedge base dir, per service/dependency (sibling to the
  existing real-DSN file convention `<baseDir>/services/<service>/<dependency>.dsn`,
  `internal/dsn/dsn.go`).
- *In-cluster deploy* — **database-backed** persistence (the fork's "state dir or database" option),
  preferred over a PersistentVolume so the store travels with the per-service database, needs no extra
  volume wiring, and is wiped together with the data by `DropDatabase` on `--clean`.

**Alternatives**: re-implement down-file capture in devedge (duplicates the fork; rejected — DRY,
maintenance); a Kubernetes PersistentVolume for the deploy-mode store (more infra, not co-existence-free,
survives `--clean` awkwardly; rejected).

## R3 — Execution model per mode (FR-003, FR-006)

**Decision**: devedge owns the migration step in both modes; the application never self-migrates on
startup.
- *Local-run* — a new schema-apply step in the daemon's dependency reconcile path
  (`internal/depruntime/reconcile.go:reconcileOne`, after `EnsureConnSecret`, before the dependency is
  marked `StateReady`; surfaced through `internal/daemon/depstore.go:DepManager.Apply`). The daemon holds
  the live supervised port-forward (`internal/cluster/portforward.go`) and the real DSN/binding, so it
  applies migrations over that connection using the fork as a library.
- *In-cluster deploy* — a Helm **`pre-install`/`pre-upgrade` hook Job** added to the embedded service chart
  (`internal/helm/charts/service/templates/migrate-job.yaml`), running the service image's `migrate`
  subcommand. devedge installs charts with `helm upgrade --install … --wait` (`internal/helm/helm.go:197`),
  and Helm runs hook Jobs to completion before the release's main resources — so the Deployment only rolls
  after migrations succeed, satisfying FR-003/FR-006 with no extra orchestration in `de`.

**Rationale**: keeps devedge as the single, observable owner of the step (Constitution V); makes the
deployed path production-faithful (migrate-then-roll); reuses existing seams (reconcile readiness; helm
`--wait`); avoids the replica-race/CrashLoop failure mode of in-app startup migration.

**Alternatives**: app-runs-migrations-on-startup (forfeits FR-003 ordering guarantee, FR-010 observability,
races across replicas; rejected); `de` runs the deploy-mode migration client-side over a port-forward
(works, but is not production-faithful and puts DDL on the dev/CI host rather than in-cluster; rejected for
deploy mode — it *is* the local-run mechanism).

## R4 — Migration-file source per mode, and the service-image contract (FR-001)

**Decision**: Migrations travel with the code.
- *Local-run* — files read from the **declared per-service host directory** (config, R6) by the daemon.
- *In-cluster deploy* — files **bundled in the service image**, executed by that image's **`migrate`
  subcommand** in the hook Job. This makes the deployed service image's `migrate` entrypoint a **contract**
  (documented in `contracts/`): it MUST accept the injected DSN (from the per-dep Secret) and the
  persisted-down-state store config, and run the fork against its bundled files.

**Rationale**: matches the clarification ("app image + migrate cmd") and version-matches migrations to the
code that needs them. The persisted-down store (R2) is what frees rollback from depending on the running
image's bundled files.

**Edge**: a deploy-mode service image **without** a `migrate` subcommand → devedge MUST fail with an
actionable error (FR-007 style), not silently skip. MVP sequencing (R9) lands local-run first, so this
contract is only required when `--deploy` + migrations are combined.

**Alternatives**: a generic `migrate/migrate` image with files mounted from a ConfigMap (decouples from the
app version, needs file-mount machinery; rejected per clarification, but kept as the documented fallback if
the image contract proves impractical).

## R5 — Seed apply-once tracking (FR-005)

**Decision**: devedge applies dev seed **after** a successful migration and records it with a **marker row
in the target database** (a small `devedge_seed` bookkeeping table devedge owns). Before seeding, devedge
checks the marker; if present, it skips. `de project down --clean` calls `DropDatabase`
(`internal/depruntime/realprov.go:230`), which removes the marker along with everything else, so the next
`up` re-seeds. Developers write plain seed SQL (no idempotency burden).

**Rationale**: the marker lives with the data it guards, is automatically reset by the existing `--clean`
drop (no new cleanup path), and survives daemon restarts (unlike in-memory daemon state). Matches
"applied once per fresh database."

**Alternatives**: track in the daemon's depstore state (lost on a fresh DB that the daemon still
remembers → drift; rejected); require developer-idempotent seed (pushes correctness onto every author;
rejected per clarification).

## R6 — Config surface (FR-001, FR-002, FR-011)

**Decision**: Extend the **`Dependency`** struct in `pkg/config/service.go` (the strict
`KnownFields(true)` decoder) with optional `migrations` (a directory path, relative to the project) and
`seed` (a file or directory path). Add a `MigrationDeclarer` capability interface in
`pkg/config/resource.go` (mirroring `DependencyDeclarer`/`WorkloadDeclarer`). `Validate` rejects
`migrations`/`seed` on a non-`postgres` engine (FR-011) and validates path presence.

**Rationale**: migrations are per-dependency (a service could in principle have more than one relational
dependency); attaching to `Dependency` keeps the association explicit and the engine-gate local. The strict
decoder means the fields must be declared here or parsing fails — consistent with the existing schema
discipline.

**Alternatives**: a top-level `spec.storage` block (further from the dependency it acts on; would need to
name which dependency anyway; rejected).

## R7 — CI seed gating (FR-013)

**Decision**: Seed is skipped when the resolved environment is ephemeral/CI. `de ci run`
(`cmd/de/ci.go`) sets `DEVEDGE_ENV=ephemeral`; the `up`/reconcile path already resolves environment via
`cluster.DetectEnvironment`. The seed step checks that resolved mode and no-ops in CI. Migrations
(schema) always run.

**Rationale**: tests own their fixtures; dev seed could pollute assertions. Reuses the existing environment
signal — no new flag.

**Alternatives**: a `--no-seed` flag (manual, error-prone; rejected as the *default* mechanism, though the
flag could be added later).

## R8 — Migration credentials / least privilege

**Decision**: For dev scope, run migrations as the **existing per-service role** that already owns its
isolated database (003, `internal/depruntime/realprov.go:ensurePostgres`). Do **not** introduce a separate
migration role in this feature.

**Rationale**: the per-service database is isolated and owned by that role; in an isolated dev DB the
owner running DDL is appropriate and adds no exposure. A separate, higher-privileged migration role vs. a
constrained runtime role is a **production** hardening concern, explicitly out of scope here and noted for
a future prod-deployment story.

**Alternatives**: split migration/runtime roles now (gold-plating for an isolated dev DB; rejected, with a
documented follow-up).

## R9 — MVP sequencing & failure handling (US priorities, FR-007)

**Decision**: Land in slices matching the spec's priorities:
1. **Local-run migrations** (US1, MVP) — config + portable applier + daemon reconcile step + idempotent
   re-run + `--clean` reset + observability. Testable entirely in local-run (matches US1 Independent Test).
2. **Both-mode ordering + deploy hook Job** (US2) — the Helm pre-install/pre-upgrade hook + service-image
   contract.
3. **Dev seed** (US3) — apply-once marker + CI gate.
Rollback/down-persistence (FR-012) is wired with the engine in slice 1 (it is a config of the same applier)
and asserted in slice 2's e2e.

Failure handling (FR-007) leans on the fork's dirty-state recovery (R2): a failed migration aborts `up`
with an actionable error and the next corrected `up` auto-recovers.

**Rationale**: each slice is independently testable and demoable (Constitution II); the MVP delivers the
core value (a usable schema) without the deploy-mode machinery.

## R10 — Performance & determinism

**Decision**: The migration step is bounded — it reuses 003's dependency-readiness wait
(bounded retry, `internal/depruntime`) before connecting, and in deploy mode the hook Job is gated by
`helm … --wait`. Rendered artifacts (the hook-Job template, values) are deterministic and inspectable
(Constitution: deterministic generated artifacts). No target latency is asserted for a dev-time one-shot
schema apply beyond "completes within the existing readiness/`up` window"; the step MUST use a bounded
timeout and surface a clear error on exceed.

**Rationale**: aligns with Constitution Performance & Reliability (bounded retries/timeouts;
restart-safe/idempotent) without inventing an arbitrary SLO for a developer one-shot operation.
