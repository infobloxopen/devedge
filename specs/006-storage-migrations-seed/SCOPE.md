# Scope diff — 006-storage-migrations-seed

Final scope gate (T022): every requirement traces to an implementation + a test, nothing was
built that does not trace to a requirement, and a service declaring no migrations/seed is
unchanged. All build/vet/`go test ./...` green; the three k3d e2es pass live.

## Functional requirements

| FR | Where satisfied | Verified by |
|----|-----------------|-------------|
| **FR-001** declare versioned migrations on the Postgres dep | `pkg/config` `Dependency.Migrations` (strict decode) + `MigrationDeclarer` | `pkg/config/service_test.go` (T003) |
| **FR-002** optionally declare dev seed | `Dependency.Seed` | `service_test.go` (T003) |
| **FR-003** apply before the workload serves | local-run: `reconcileOne` runs migrate before the DSN/env-var is emitted & before `StateReady`; deploy: Helm `pre-install/pre-upgrade` hook Job gated by `helm --wait` | `migrations_local_test.go` (T008), `migrations_deploy_test.go` (T012) |
| **FR-004** idempotent | applier returns `AlreadyCurrent` when already at target | `applier_test.go` (T004), local e2e §2 |
| **FR-005** seed once, recorded; `--clean` resets | `internal/migrate/seed.go` `devedge_seed` marker table; `--clean` drops the DB+marker | `applier_test.go`, `migrations_seed_test.go` (T017) |
| **FR-006** both modes | one `Applier`: daemon (local) + service-image `migrate` (deploy) | local + deploy e2es |
| **FR-007** failure aborts; common-case recovery, no manual cleanup | `reconcileOne` holds out of Ready + aborts on migrate error; fork dirty-state recovery | local e2e §3 (**see deviation D3**) |
| **FR-008** `--clean` clears schema+seed; plain `down` preserves | `Reconciler.Release` (DropDatabase + host down-store removal); deploy: PVC removed on `--clean` | local e2e §4 (preserve) / §5 (clean) |
| **FR-009** operate only within the per-service isolated DB | reuses the 003 per-service binding; no cross-service effect | local e2e §6 (two-service isolation) |
| **FR-010** report migration + seed outcome | `Result.Migration`/`Result.Seed`; CLI lines + daemon logs (T010/T019); applier slog (T020) | local/seed e2es assert outcomes |
| **FR-011** Postgres only; Redis unaffected | `ServiceConfig.Validate` engine-gate | `service_test.go` (engine-gate cases) |
| **FR-012** persist down steps; rollback survives image change | persisted down-store as the migration source (host dir / deploy PVC); applier seeds it additively | deploy e2e §T013 (rollback via PVC after a down-less image), local e2e §7 (rollback after source removal) |
| **FR-013** seed in dev only, never in CI | reconcile skips seed when env is `ephemeral`; env resolved CLI-side and threaded via the apply request | seed e2e §4 (CI skip) |

## Success criteria

| SC | Verified by |
|----|-------------|
| **SC-001** schema at declared version after `up` | local e2e §1 (table exists), deploy e2e (hook applied) |
| **SC-002** idempotent re-run, zero changes | local e2e §2, `applier_test.go` |
| **SC-003** first dependent query succeeds, both modes | local e2e (psql query), deploy e2e (dependent query post-deploy) |
| **SC-004** failure: actionable error, no serve on partial schema; corrected re-run succeeds | local e2e §3 (**deviation D3**: "succeeds" = recovers to a clean state without manual steps; the definitive corrected-schema path is `--clean`+`up`) |
| **SC-005** `--clean` removes schema+seed; `up` rebuilds | local e2e §5 (schema), seed e2e §3 (seed re-applied) |
| **SC-006** two co-located services isolated | local e2e §6 |
| **SC-007** rollback across an image lacking the down files | deploy e2e §T013 |

## Deviations from the written design (all recorded in tasks.md, approved/justified)

- **D1 — fork consumption:** consumed via a `go.mod` `replace` of `golang-migrate/migrate/v4` → the
  org-pinned `infobloxopen/migrate/v4` pseudo-version (the fork keeps the upstream module path, so research
  R1's "clean require" was impossible). `dirtyStateConf` verified present in the pin.
- **D2 — down-store is filesystem-only (not "database-backed"):** the fork exposes no DB-backed store
  (contradicting research R2 / data-model §3). Per the spec owner: local-run uses a host dir; deploy uses a
  per-service **PVC** devedge side-provisions (and removes on `--clean`). FR-012 is satisfied via the PVC.
- **D3 — migrate targets a version (up or down); recovery is "common case":** per the spec owner, the step
  converges to the source/image's version using the persisted store as the migration source — deploying an
  older image rolls back automatically (resolves the "what triggers a down" gap). FR-007/SC-004 recovery is
  scoped (by the spec's "for the common case" wording) to un-wedging a dirty DB without manual `migrate
  force`; a transactionally-rolled-back bad migration is fixed by `--clean`+`up`. Documented in README/CHANGELOG.
- **D4 — `MigrationDeclarer.Migrations(projectDir)`** takes the project dir (contract C4's no-arg form was
  illustrative; `ServiceConfig` stores no source path and the codebase resolves project-relative paths at
  point-of-use).

## No gold-plating

The change is additive to config, the daemon reconcile path, the service Helm chart (one hook-Job
template), the deploy plumbing, and one new package (`internal/migrate`, justified by Constitution IV).
No speculative abstraction or unused extension points: the only new `Provisioner` method
(`EnsureMigrationStore`) and the only new chart values exist to serve FR-006/FR-012; the `Applier`/`Source`/
`DownStore`/`Result`/`SeedOutcome` types each back a tested requirement. The deploy-mode test image lives
under `test/e2e/testdata` (excluded from the product build).

## Zero impact when nothing is declared (FR-003 / US2-AS3)

A dependency with no `migrations`/`seed` skips the migrate step (`if d.Migrations != ""`), the seed step
(`if d.Seed != ""`), and the PVC provisioning; `chartValues` emits no `migrations` block, so
`templates/migrate-job.yaml` renders empty (verified: `helm template` → 0 Jobs; `TestChartValues` asserts no
block). A `kind: Config` resource or a no-migrations Service behaves exactly as before this feature.
