# Contracts: Schema migrations and dev seed on `up`

devedge is a CLI + daemon, not a network service; the "interfaces" this feature exposes are: (C1) the
`devedge.yaml` config surface (developer-facing), (C2) the service-image `migrate` subcommand contract
(image-author-facing, deploy mode), (C3) the Helm pre-install/pre-upgrade hook Job rendered into the
service chart, and (C4) the internal applier interface that keeps migration logic portable and testable
(Constitution IV).

---

## C1 — `devedge.yaml` config (developer-facing)

Additive, optional fields on a `postgres` dependency. See `data-model.md §1` for the struct + validation.

```yaml
dependencies:
  - name: db
    engine: postgres
    migrations: db/migrations     # optional; dir of NNN_name.up.sql / NNN_name.down.sql
    seed: db/seed/dev.sql         # optional; plain SQL file or dir; local/dev only
```

**Guarantees**: absent fields → no behavior change. `migrations`/`seed` on a non-postgres engine →
parse/validate error. Paths resolve under the project root and must exist.

---

## C2 — Service-image `migrate` subcommand (deploy mode; image-author-facing)

In deploy mode the Helm hook Job runs the **service's own image** to apply migrations. The image MUST
provide a `migrate` entrypoint/subcommand with this contract:

**Invocation** (by the hook Job):
```
<image> migrate up
```

**Inputs** (environment, injected by the chart):
| Var | Source | Meaning |
|-----|--------|---------|
| `DATABASE_URL` (or the agreed key) | the per-dep DSN Secret `<service-slug>-<dep>-dsn`, key `dsn` (003/005) | in-cluster Postgres DSN |
| down-state store config | chart value (DB-backed; see R2) | where applied up/down files are persisted |

**Behavior the subcommand MUST honor**:
- Apply all not-yet-applied bundled migrations forward to latest (idempotent; no-op if current).
- Use the `github.com/infobloxopen/migrate` engine with the **persisted-down-state** config enabled, so
  applied downs are stored independent of the image (FR-012) and a dirty DB auto-recovers (FR-007).
- Exit **0** on success (schema current), **non-zero** with a clear message on failure (gates the rollout).

**Failure**: if the deployed image does **not** provide this subcommand, devedge MUST surface an
actionable error when `--deploy` is combined with declared migrations (R4) — it MUST NOT silently skip.

> Local-run mode does **not** use C2: the daemon applies migrations as a library over the supervised
> port-forward (C4), reading files from the host `migrations` dir.

---

## C3 — Helm hook Job (rendered into the service chart)

New template `internal/helm/charts/service/templates/migrate-job.yaml`, rendered only when a `migrations`
value is present. Shape (illustrative; exact fields finalized in implementation):

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: {{ .Values.service.name }}-migrate
  annotations:
    "helm.sh/hook": pre-install,pre-upgrade
    "helm.sh/hook-weight": "-5"
    "helm.sh/hook-delete-policy": before-hook-creation,hook-succeeded
spec:
  backoffLimit: 1
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: migrate
          image: {{ .Values.service.image }}          # same image as the workload
          args: ["migrate", "up"]
          env:
            - name: DATABASE_URL
              valueFrom:
                secretKeyRef: { name: {{ .Values.service.name }}-{{ .dep }}-dsn, key: dsn }
            # + down-state store config (DB-backed)
```

**Guarantees**: Helm runs the hook to completion before the release's Deployment (the `--wait` install at
`internal/helm/helm.go:197`), so the workload rolls only against a migrated schema (FR-003/FR-006). The
`hook-delete-policy` keeps reruns clean; `backoffLimit` bounds retries. Rendered output is deterministic
(Constitution).

**Values plumbed** via `internal/deploy/deploy.go:chartValues` + `deploy.Workload`: `migrations` presence,
the dep DSN secret name, and the down-state store config.

---

## C4 — Internal applier interface (portable seam; Constitution IV)

A new portable package (e.g. `internal/migrate`) exposes the migration/seed logic behind an interface so
the core is unit-testable with a fake and the fork dependency is isolated:

```go
// Applier brings a target database to the declared schema and (optionally) seeds it.
type Applier interface {
    // Migrate applies all pending migrations from src to the database at dsn, persisting
    // applied down steps to the configured store. Idempotent; recovers a dirty DB.
    Migrate(ctx context.Context, dsn string, src Source, store DownStore) (Result, error)

    // Seed applies seed once per fresh database, keyed by a marker; no-op if already applied.
    // Skipped entirely by the caller in CI/ephemeral mode (FR-013).
    Seed(ctx context.Context, dsn string, seed Source) (Seeded bool, err error)
}

type Result struct {
    FromVersion uint
    ToVersion   uint
    Applied     int
    AlreadyCurrent bool
}
```

- The **real** implementation wraps `github.com/infobloxopen/migrate` (R1/R2).
- A **fake** implementation backs unit/integration tests without a real DB (mirrors the existing
  `internal/depruntime/fake_test.go` pattern).
- **Callers**: the daemon reconcile path (local-run; `internal/depruntime/reconcile.go` /
  `internal/daemon/depstore.go`) and — for deploy mode — the same logic compiled into the service image's
  `migrate` subcommand (C2). The interface is the single definition of "apply migrations" shared by both
  modes (FR-006).

**Test contract** (drives `/speckit.tasks` test tasks):
- `Migrate` is idempotent: second call with no new files → `AlreadyCurrent`, zero applied (SC-002).
- A failed migration leaves recoverable state; a corrected re-run succeeds (FR-007, SC-004).
- Down steps persisted by an earlier apply remain usable after the source files are removed (FR-012,
  SC-007).
- `Seed` applies once; second call no-ops; reset after marker cleared (SC-005).
