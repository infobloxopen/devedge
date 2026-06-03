# Implementation Plan: Schema migrations and dev seed on `up`

**Branch**: `006-storage-migrations-seed` | **Date**: 2026-06-02 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/006-storage-migrations-seed/spec.md`

## Summary

Give a devedge service a **usable** database, not just an empty one. A service declares versioned schema
migrations (and optional dev seed) on its Postgres dependency; `de project up` brings the schema to the
declared version — and applies seed — **before the workload serves**, in both the local-run inner loop and
the in-cluster deploy mode (005), idempotently, with `down --clean` resetting it.

**Technical approach** (from research): adopt the **`github.com/infobloxopen/migrate`** fork (branch `ib`)
as the engine — its `dirtyStateConf` mechanism (PR #57) persists applied up/down files to a state store and
auto-recovers a dirty DB, delivering FR-012 (rollback survives image changes) and FR-007 (corrected re-run
recovers) for free. devedge owns the step in both modes: **local-run** applies migrations as a library in
the daemon reconcile path over the existing supervised port-forward; **deploy** runs them in a Helm
`pre-install`/`pre-upgrade` hook Job (the service image's `migrate` subcommand), gated before the
Deployment rolls by `helm --wait`. Seed is applied once per fresh DB via a marker row and skipped in CI.
Migration logic lives behind a portable `Applier` interface (fake-testable); the fork and the Helm/Job
rendering are the only new boundary surfaces.

## Technical Context

**Language/Version**: Go 1.25.5 (module `github.com/infobloxopen/devedge`)
**Primary Dependencies**: `spf13/cobra` (CLI), `gopkg.in/yaml.v3` (strict config decode), `helm` CLI
subprocess (no Go SDK; embedded charts via `//go:embed`), `kubectl`/`k3d`/`docker`/`psql` (cluster + e2e).
**NEW**: `github.com/infobloxopen/migrate@ib` (migration engine, library + image subcommand).
**Storage**: Postgres (the per-service isolated DB from 003). New state: the engine's `schema_migrations`
table; a devedge-owned `devedge_seed` marker table; the persisted down-migration store (host dir for
local-run, DB-backed for deploy).
**Testing**: `go test ./...` (unit/integration with a fake applier, mirroring
`internal/depruntime/fake_test.go`); k3d e2e gated by `DEVEDGE_E2E=1` (`test/e2e/`), modeled on
`dependency_postgres_test.go` (local) and `workload_deploy_test.go` (deploy).
**Target Platform**: macOS + Linux dev hosts; k3d clusters (shared dev / ephemeral CI).
**Project Type**: single Go project, multi-binary (`de`, `devedged`, `devedge-dns-webhook`) — CLI + daemon.
**Performance Goals**: dev-time one-shot apply; no SLO beyond "completes within the existing
dependency-readiness / `up` window"; bounded timeout + clear error on exceed (R10).
**Constraints**: zero behavior change for services that declare nothing (FR-003/US2-AS3); deterministic
rendered artifacts; co-existence-safe; never mutate the current kube-context (004 invariant); strict config
decoder (new fields must be declared or parsing fails).
**Scale/Scope**: one service per `up`; small dev migration sets.

## Constitution Check

*GATE: must pass before Phase 0 and re-checked after Phase 1. Devedge Constitution v1.0.0.*

| Principle | Assessment | Status |
|-----------|------------|--------|
| **I. Edge-First DX** | Opt-in (only when declared); one command (`de project up`); idempotent + safe to repeat; no new manual `psql`/`helm`/`kubectl`. | ✅ Pass |
| **II. Spec-Driven, Test-Driven** | spec + clarify complete; FR/SC trace to tasks; tests-first (Applier fake unit tests, then integration, then k3d e2e); each FR maps to a test (contracts C4). | ✅ Pass |
| **III. E2E Confidence over Mocked Comfort** | Touches persistence + cluster integration → **mandatory** k3d e2e: local-run apply/idempotency/`--clean` (model `dependency_postgres_test.go`) + deploy-mode hook-Job schema-before-serve (model `workload_deploy_test.go`) + a rollback-across-image assertion (SC-007). | ✅ Pass |
| **IV. Portable Core, Explicit Adapters** | Migration/seed logic behind a portable `Applier` interface (`internal/migrate`); the fork, the Helm hook-Job rendering, and `KubectlExec`/`psql` are adapter-side; core unit-tested with a fake. | ✅ Pass |
| **V. Safe Reconciliation & Observable Ops** | Desired vs observed = declared version vs `schema_migrations` version; reports version reached / applied count / seed outcome (FR-010); abort-on-failure + dirty-state recovery (FR-007); structured logs; readiness gates serving. | ✅ Pass |

**Engineering standards**: Go (✓); deterministic generated artifacts — the hook-Job template + values are
inspectable/diffable (✓); new external dependency justified — the fork is the org-standard engine and the
only way to get the FR-012/FR-007 behavior without re-implementing it (✓); bounded retries/timeouts (R10,
reuse 003 readiness) (✓). **No violations → Complexity Tracking empty.**

## Project Structure

### Documentation (this feature)

```text
specs/006-storage-migrations-seed/
├── spec.md              # /speckit.specify + /speckit.clarify
├── plan.md              # this file
├── research.md          # Phase 0 (R1–R10)
├── data-model.md        # Phase 1 (config, state, status)
├── quickstart.md        # Phase 1 (developer usage + acceptance map)
├── contracts/
│   └── migrations-contract.md   # C1 config · C2 image migrate cmd · C3 hook Job · C4 Applier
├── checklists/
│   └── requirements.md  # spec quality checklist (from /speckit.specify)
└── tasks.md             # /speckit.tasks (NOT created here)
```

### Source code (real devedge paths to touch)

```text
pkg/config/
├── service.go                 # + Dependency.Migrations/Seed; Validate engine-gate (FR-001/002/011)
└── resource.go                # + MigrationDeclarer capability interface

internal/migrate/              # NEW portable package (Constitution IV)
├── applier.go                 # Applier interface, Result, Source, DownStore (contracts C4)
├── migrate_fork.go            # real impl over github.com/infobloxopen/migrate (R1/R2)
├── seed.go                    # seed apply-once via devedge_seed marker (R5)
└── *_test.go                  # unit tests with a fake DB/applier

internal/depruntime/
├── reconcile.go               # reconcileOne: schema/seed step after EnsureConnSecret, before StateReady
└── realprov.go                # DropDatabase already wipes schema/seed; (host down-store reset on release)

internal/daemon/
└── depstore.go                # DepManager.Apply surfaces the schema/seed step; Release resets on --clean

internal/helm/charts/service/templates/
└── migrate-job.yaml           # NEW pre-install/pre-upgrade hook Job (deploy mode; contracts C3)

internal/deploy/
└── deploy.go                  # chartValues: plumb migrations presence + DSN secret + down-store config

cmd/de/
├── main.go                    # up/down wiring; seed CI-gate via env; down --clean reset
└── dependencies.go            # provisionDependencies → invoke local-run apply

test/e2e/                      # NEW (DEVEDGE_E2E=1)
├── migrations_local_test.go   # local-run: apply, idempotent re-run, --clean reset, failure recovery
└── migrations_deploy_test.go  # deploy hook: schema before serve; rollback-across-image (SC-007)
```

**Structure Decision**: single Go project; extend existing seams identified in the code map. The only new
package is `internal/migrate` (the portable applier), justified by Constitution IV (isolate the fork +
keep the core fake-testable). Everything else is additive edits to config, the daemon reconcile path, the
service Helm chart, and the deploy plumbing.

### Implementation slices (sequencing for `/speckit.tasks`; from R9)

1. **MVP — local-run migrations (US1)**: config + `Applier` (+ fork wiring incl. persisted-down store) +
   daemon reconcile step + idempotent re-run + observability + `--clean` reset + unit/integration + local
   e2e. Delivers a usable schema end-to-end without deploy machinery.
2. **Both-mode + deploy hook (US2)**: `migrate-job.yaml` hook + chart values + service-image `migrate`
   contract (C2) + deploy e2e (schema-before-serve, rollback-across-image SC-007).
3. **Dev seed (US3)**: `devedge_seed` marker + CI gate (FR-013) + seed e2e.

## Complexity Tracking

*No constitutional violations — no entries.*
