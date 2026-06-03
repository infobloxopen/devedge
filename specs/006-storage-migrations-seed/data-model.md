# Phase 1 Data Model: Schema migrations and dev seed on `up`

Entities are (a) **config** the developer declares, (b) **state** devedge maintains to make the operation
idempotent and reversible, and (c) the **observable status** it reports. No new wire/API types beyond the
`devedge.yaml` schema additions.

## 1. Config additions (`pkg/config/service.go`)

Added to the existing `Dependency` struct (strict `KnownFields(true)` decoder). Both fields optional;
absence == no behavior change (FR-003/US2-AS3).

| Field | YAML key | Type | Notes |
|-------|----------|------|-------|
| `Migrations` | `migrations` | `string` | Project-relative path to the migrations **directory** (golang-migrate-style `NNN_name.up.sql` / `.down.sql`). Optional. |
| `Seed` | `seed` | `string` | Project-relative path to a seed **file or directory** (plain SQL). Optional. |

**Capability interface** (`pkg/config/resource.go`), mirroring `DependencyDeclarer`/`WorkloadDeclarer`:

```go
// MigrationDeclarer is implemented by resources whose dependencies declare schema migrations/seed.
type MigrationDeclarer interface {
    // Migrations returns, per dependency name, the resolved migration/seed sources (empty if none).
    Migrations() []DependencyMigrations
}

type DependencyMigrations struct {
    Dependency string // dependency name (must be a postgres engine)
    Dir        string // absolute migrations dir (resolved from project root), "" if none
    Seed       string // absolute seed path, "" if none
}
```

**Validation rules** (`ServiceConfig.Validate`):

- `migrations`/`seed` MAY appear **only** on a dependency with `engine: postgres` → else error (FR-011).
- When set, the path MUST resolve under the project directory and exist at parse time → actionable error.
- A migrations dir with no `*.up.sql` files is an error (declared-but-empty is a mistake).
- `seed` without `migrations` is allowed (seed-only), but seed runs only after migrations (a no-op set).

### Example `devedge.yaml`

```yaml
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata: { name: widgets }
spec:
  dev: { hostname: widgets.dev.test }
  workload: { build: { context: . }, port: 8080 }   # 005
  dependencies:
    - name: db
      engine: postgres
      migrations: db/migrations      # NNN_*.up.sql / NNN_*.down.sql
      seed: db/seed/dev.sql          # optional, local/dev only
```

## 2. Migration version state (owned by the engine)

The standard golang-migrate **`schema_migrations`** table in the target per-service database
(`version BIGINT`, `dirty BOOLEAN`). devedge does not define it; the fork manages it. It is the source of
truth for "current schema version" and the `dirty` flag that R2's recovery acts on.

- *Transitions*: `(version=N, dirty=false)` → run migration N+1 → `(N+1, dirty=true)` during apply →
  `(N+1, dirty=false)` on success. A crash leaves `dirty=true`; the fork's `handleDirtyState()` recovers
  on the next run (FR-007).

## 3. Persisted down-migration store (R2; FR-012, SC-007)

The fork's `dirtyStateConf` store, holding the **up *and* down files of applied migrations**, keyed by
version, so reversal does not depend on the running image's source tree.

| Mode | Location | Lifecycle |
|------|----------|-----------|
| Local-run | host dir under devedge base, per service/dep (sibling to `<baseDir>/services/<service>/<dep>.dsn`) | `copyFiles` on apply; `cleanupFiles(version)` trims to current; removed when the dep footprint is released |
| In-cluster deploy | **database-backed** (in the per-service DB) | travels with the data; wiped by `DropDatabase` on `--clean` |

- *Invariant*: for every applied-but-not-yet-reversed version, its down step exists in the store.
- *Reset*: `--clean` (drop DB / remove host dir) empties it; next `up` repopulates from the current source.

## 4. Seed-applied marker (R5; FR-005)

A devedge-owned bookkeeping table in the target per-service database:

```
devedge_seed ( seed_fingerprint TEXT PRIMARY KEY, applied_at TIMESTAMPTZ )
```

- *Write*: after a seed applies successfully, insert a row keyed by a fingerprint of the resolved seed
  source (so changing the seed file is a distinct, re-appliable identity if desired; v1 may use a constant
  key — exactly-once-per-fresh-DB).
- *Read*: before seeding, if a matching row exists → skip (idempotent re-`up`, FR-005).
- *Reset*: `DropDatabase` on `--clean` removes the table with the data → next `up` re-seeds.
- *CI*: never written/read in ephemeral/CI mode (seed step is skipped entirely, FR-013).

## 5. Database readiness (spec Key Entity)

The condition the dependency must reach before the workload serves. Refines 003's `StateReady`:

```
provisioned (003) → schema-at-declared-version (this feature) → seeded-if-declared-and-not-CI → READY
```

- *Local-run*: the daemon advances the dependency to `READY` only after the schema/seed steps succeed
  (`reconcileOne`), so the developer's env-var/DSN is emitted against a ready DB.
- *Deploy*: the Helm pre-install/pre-upgrade hook Job embodies the gate — the Deployment rolls only after
  it completes (`helm --wait`).
- *Failure*: any step failing holds the dependency out of `READY`, aborts `up`, and reports an actionable
  error (FR-007); nothing serves against a partial schema.

## 6. Observable status (FR-010)

Reported by `de project up`: per dependency — schema **version reached** (or "already current" / "N
migrations applied"), and the **seed outcome** ("seeded" / "already seeded" / "skipped (CI)"). Emitted via
the existing structured logging/status path (Constitution V: desired vs observed distinguishable).
