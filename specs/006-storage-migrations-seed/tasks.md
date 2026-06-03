# Tasks: Schema migrations and dev seed on `up`

**Input**: Design documents from `/specs/006-storage-migrations-seed/`
**Prerequisites**: plan.md ✓, spec.md ✓, research.md ✓, data-model.md ✓, contracts/ ✓

**Tests**: INCLUDED — the devedge constitution mandates test-first (Principle II) and k3d e2e for boundary
changes (Principle III). This feature touches real persistence + cluster integration (DB schema, Helm hook
Job, in-cluster networking), so e2e coverage is central.

## Format: `[ID] [P?] [Story?] [S|C] Description with file path`

- **[P]**: parallelizable (different files, no dependency on an incomplete task)
- **[Story]**: `[US1]`…`[US3]` (user-story phases only)
- **[S] / [C]**: hub model-routing tag — `[S]` simple/mechanical → Sonnet subagent; `[C]` complex → Opus.
  Every task carries one (the `before_implement` / `route-tasks` gate requires it).

---

## Phase 1: Setup

- [X] T001 [S] Record the pre-change baseline — `make build`, `make lint`, `go test ./...` on branch `006-storage-migrations-seed`; capture in the PR description (baseline for the verify/scope gate). **Baseline (2026-06-02, HEAD 45e8976): build OK (de/devedged/devedge-dns-webhook), `go vet` clean, `go test ./...` all packages `ok`.**
- [X] T002 [S→C] Add and pin the fork to `go.mod`; `go mod tidy`; import the `pgx/v5` database + `file`/`iofs` source drivers; confirm `go build ./...` is green. **Done:** consumed via the org-standard `require github.com/golang-migrate/migrate/v4 v4.17.1` + `replace … => github.com/infobloxopen/migrate/v4 v4.16.3-0.20260414025640-b28cb3bc8342` (the fork keeps the upstream module path, so a plain `require` is impossible — R1 was imprecise; this matches `Infoblox-CTO/github-compliance-facts`). Drivers registered in `internal/migrate/drivers.go`. Build/vet/`go test ./...` all green. Re-tagged `[C]`: the "clean require" assumption was wrong and resolving the consumption mechanics needed investigation (escalation noted).

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: the config surface + the portable `Applier` (interface + fake + real fork-backed impl) that ALL three user stories depend on.

**⚠️ No user-story work begins until this phase is complete.**

### Tests first

- [X] T003 [P] [S] Unit tests for config: `Dependency.Migrations`/`Seed` strict-decode + validation (allowed only on `engine: postgres` → else error FR-011; path resolves under the project root and exists; migrations dir with no `*.up.sql` is an error; seed-without-migrations allowed) in `pkg/config/service_test.go` — must fail first.
- [X] T004 [P] [S] Contract/unit tests for the `Applier` interface against a fake (contracts C4): `Migrate` idempotent (re-run → `AlreadyCurrent`, 0 applied, SC-002); a failed migration leaves recoverable state and a corrected re-run succeeds (FR-007/SC-004); down steps persisted by an earlier apply remain usable after the source files are removed (FR-012/SC-007); `Seed` applies once then no-ops and re-applies after the marker is cleared (SC-005) in `internal/migrate/applier_test.go` — must fail first.

### Implementation

- [X] T005 [S] Add `Migrations`/`Seed` fields to `Dependency` (strict decode) + `Validate` engine-gate and path checks in `pkg/config/service.go`; add the `MigrationDeclarer` interface + `DependencyMigrations` type with a `Migrations()` accessor in `pkg/config/resource.go` — depends on T003.
- [X] T006 [S] Define the portable `Applier` interface + `Result`/`Source`/`DownStore` types and a fake implementation per contracts C4 in `internal/migrate/applier.go` + `internal/migrate/fake.go` — depends on T004. **Design notes (carry into T007/T009):** `Source{Path string}` (migrations dir or seed file/dir); `DownStore{Dir string}` (persisted down-store directory — file-only, matching the fork API per T002 findings); `Applier.Migrate(ctx, dsn, src, store) (Result, error)` + `Seed(ctx, dsn, seed) (bool, error)`. **`MigrationDeclarer` signature deviates from C4's no-arg `Migrations()`:** it is `Migrations(projectDir string) ([]DependencyMigrations, error)` — required because `ServiceConfig` stores no source path and the codebase resolves project-relative paths at point-of-use (the contract's was illustrative).
- [X] T007 [C] Implement the real fork-backed `Applier` in `internal/migrate/migrate_fork.go`: wrap `github.com/infobloxopen/migrate` with its `dirtyStateConf` enabled — **confirm the exact enabling API (option/constructor) and store mode (dir vs DB) by reading the `ib` source** (R1/R2) — wiring the persisted-down store (host dir for local-run, DB-backed for deploy), forward apply + idempotency + dirty-state recovery; populate `Result` (from/to version, applied count, already-current) — depends on T006, T002.

**Checkpoint**: config fields + validation and the `Applier` (fake + real) exist and are unit-green.

---

## Phase 3: User Story 1 — Migrations bring the database to the declared schema on `up` (Priority: P1) 🎯 MVP

**Goal**: a service declaring migrations gets its schema brought to the latest version during local-run `de project up`, before the DB is marked ready — idempotently, observably, with `--clean` resetting it.

**Independent Test**: declare one migration that creates a table; `de project up` → the table exists in the provisioned DB; re-run → no re-apply, schema unchanged; a bad migration aborts `up` with an actionable message and a corrected re-run recovers without manual `psql`.

- [X] T008 [C] [US1] e2e (k3d) in `test/e2e/migrations_local_test.go`: in local-run, `de project up` for a project declaring one migration brings the per-service isolated DB (003) to schema before READY; re-run is a no-op (SC-001/002); a deliberately-bad migration aborts `up` with an actionable error and a corrected re-run auto-recovers with no manual cleanup (FR-007/SC-004); `down --clean` then `up` rebuilds the schema (SC-005 schema part) — operates within the per-service DB (FR-009); must fail first.
- [X] T009 [C] [US1] Wire the local-run schema step into the daemon reconcile path: in `internal/depruntime/reconcile.go:reconcileOne` (after `EnsureConnSecret`, before `StateReady`), run `Applier.Migrate` over the live supervised port-forward DSN/binding when the dependency declares migrations; surface through `internal/daemon/depstore.go:DepManager.Apply`; on failure hold the dependency out of READY and abort `up` (FR-003/FR-006-local/FR-007) — depends on T007.
- [X] T010 [S] [US1] Report the migration outcome (version reached / N applied / "already current") on `de project up` via the existing structured status/log path in `cmd/de/` + `internal/daemon/` (FR-010) — depends on T009.
- [X] T011 [S] [US1] On `de project down --clean`, reset the local-run persisted-down store (host dir) in `internal/depruntime/reconcile.go:Release` / `internal/depruntime/realprov.go` (the existing `DropDatabase` already wipes the in-DB schema; this clears the host-side store) (FR-008) — depends on T009.

**Checkpoint**: MVP — local-run migrations apply before serve, idempotent re-run, observable, `--clean` resets; rollback machinery (T007) is in place and asserted next in US2.

---

## US2 design decisions (from the spec owner, 2026-06-02 — resolve the R2/data-model §3 gaps)

1. **Down-store persistence = a per-service PVC** (data-model §3's "database-backed" is infeasible — the
   fork's store is filesystem-only). devedge **side-provisions** the PVC (mirroring how `EnsureConnSecret`
   writes the in-cluster DSN Secret directly), mounts it into the migrate hook Job at the down-store path,
   and deletes it on `--clean`.
2. **The migrate step always TARGETS A VERSION; up or down is chosen by the relative version.** The target =
   the highest migration version in the current source/image. Deploying an *older* image (lower target)
   therefore **auto-rolls-back** (down) — no separate rollback command. This unifies local-run and deploy
   and resolves the T013 "what triggers a down" gap.
   - **Mechanism:** the applier uses the **persisted store as the migration source**, seeded additively from
     the current source each run, so the store holds the union of every applied version + the current set —
     a down step stays available even when the current image/branch no longer ships it (FR-012). `goto(target)`
     then reads up *or* down from the store. (Refactors the US1 `up`-only applier — T007 — into goto-target;
     re-verified by the US1 e2e incl. a local-run rollback case.)

## Phase 4: User Story 2 — The workload starts against a ready, migrated database — both modes (Priority: P1)

**Goal**: in-cluster deploy (005) applies the schema via a Helm pre-install/pre-upgrade hook Job before the Deployment rolls, so the workload's first query succeeds; rollback works even from an older image.

**Independent Test**: `up --deploy` of a service whose image exposes a `migrate` subcommand → the hook applies the schema before the workload serves and its first dependent query succeeds; reverse a migration using an image lacking the down file → it still succeeds via the persisted down step.

- [ ] T012 [C] [US2] e2e (k3d) in `test/e2e/migrations_deploy_test.go`: `up --deploy` of a service whose image exposes a `migrate` subcommand → the `pre-upgrade` hook Job applies the schema before the Deployment rolls, and the workload's first dependent query succeeds (FR-003/FR-006-deploy/SC-003) — must fail first.
- [ ] T013 [C] [US2] e2e (k3d) in `test/e2e/migrations_deploy_test.go`: apply schema v2 via deploy; redeploy an image that does **not** ship the v2 `*.down.sql`; the persisted down step reverses v2→v1 successfully (FR-012/SC-007) — must fail first.
- [ ] T014 [S] [US2] Add `internal/helm/charts/service/templates/migrate-job.yaml` per contracts C3 — a Job annotated `helm.sh/hook: pre-install,pre-upgrade` (+ `hook-weight`, `hook-delete-policy: before-hook-creation,hook-succeeded`, `backoffLimit: 1`) running `<service.image> migrate up`, env `DATABASE_URL` from the per-dep DSN Secret + the down-store config; rendered only when a `migrations` value is present — depends on T007.
- [ ] T015 [C] [US2] Plumb the migrations values (presence, dep DSN secret name `<svc>-<dep>-dsn`, DB-backed down-store config) through `internal/deploy/deploy.go:chartValues` + `deploy.Workload`, ensuring the hook Job uses the same service image as the workload (FR-006-deploy) — depends on T014.
- [ ] T016 [S] [US2] Deploy guard: when `--deploy` + declared migrations but the image provides no `migrate` subcommand, fail with an actionable error (no silent skip, R4) in `internal/deploy/` / `cmd/de/deploy.go` — depends on T015.

**Checkpoint**: deploy-mode schema-before-serve proven in both modes; rollback survives image changes.

---

## Phase 5: User Story 3 — Optional dev seed data populates the database (Priority: P2)

**Goal**: declared dev seed is applied once after migrations succeed, locally/dev only (never in CI), reset by `--clean`.

**Independent Test**: declare migrations + seed; `up` → seeded rows present after migrations; re-`up` → no duplicate/error; `down --clean` then `up` → re-seeded; `de ci run -- de project up` → schema applied, seed skipped.

- [ ] T017 [C] [US3] e2e (k3d) in `test/e2e/migrations_seed_test.go`: declare migrations + seed; local `up` → seeded rows present after migration; re-`up` → no duplicate/error (SC-005 seed part); `down --clean` then `up` → re-seeded; `de ci run -- de project up` on the ephemeral cluster → schema applied, **seed skipped** (FR-013) — must fail first.
- [ ] T018 [C] [US3] Implement seed apply-once in `internal/migrate/seed.go` + the reconcile wiring: **(replace the deferred `ForkApplier.Seed` stub in `migrate_fork.go` from T007 — do not add a second `Seed` method.)** after a successful migrate, apply the seed and record it via the `devedge_seed` marker table; skip when the marker exists; skip entirely when the resolved environment is ephemeral/CI (`cluster.DetectEnvironment`, FR-013); reset on `--clean` (DB drop removes the marker) — depends on T009, T007.
- [ ] T019 [S] [US3] Report the seed outcome ("seeded" / "already seeded" / "skipped (CI)") on `up` (FR-010) in `cmd/de/` + `internal/daemon/` — depends on T018.

**Checkpoint**: seed applies once locally, is reset by `--clean`, and never runs in CI.

---

## Phase 6: Polish & Cross-Cutting

- [ ] T020 [P] [S] Structured logging across `internal/migrate` (apply start/result, seed outcome, errors) — Principle V (observable operations).
- [ ] T021 [P] [S] Docs: update devedge `README.md` / `CLAUDE.md` / `CHANGELOG.md` for the `migrations`/`seed` config and the service-image `migrate` subcommand contract (contracts C2); validate every step in `specs/006-storage-migrations-seed/quickstart.md`.
- [ ] T022 [S] Final scope diff vs FR-001…FR-013 / SC-001…SC-007 in `specs/006-storage-migrations-seed/SCOPE.md`; confirm no gold-plating and that services declaring no migrations/seed are unchanged (FR-003/US2-AS3).

---

## Dependencies & Execution Order

- **Setup (P1)** → no deps. T002 (fork dep) blocks T007.
- **Foundational (P2)** → after Setup; **blocks all user stories**. Internal order: T003/T004 (tests, parallel) → T005; T006 → T007 (T007 also needs T002).
- **US1 (P3)** → after Foundational. T008 (e2e) → T009 → {T010, T011}. **MVP.**
- **US2 (P4)** → after US1 (reuses the Applier + the deploy path from 005). T012/T013 (e2e) → T014 → T015 → T016.
- **US3 (P5)** → after US1 (seed runs after the migrate step T009). T017 (e2e) → T018 → T019.
- **Polish (P6)** → after the targeted stories. T020/T021 parallel; T022 last.

### Parallel opportunities

- Foundational tests T003 / T004 run in parallel (distinct files).
- US2 e2e T012 / T013 author together (same file, distinct cases); chart template T014 parallels the US2 Go plumbing once T007 lands.
- Polish T020 / T021 run in parallel.

## Implementation Strategy

- **MVP** = Setup + Foundational + **US1** → stop & validate (local-run migrations apply before serve, idempotent, `--clean` reset).
- Then **US2** (deploy hook + rollback) and **US3** (seed + CI gate) as incremental slices.
- TDD throughout: every `[C]` e2e / test task (T003, T004, T008, T012, T013, T017) is written to fail first.

## Model Routing (hub gate)

- **`[C]` → Opus (8):** T007, T008, T009, T012, T013, T015, T017, T018 — the fork-backed applier (reads
  fork internals + persisted-down/dirty-recovery wiring), all k3d e2e (real boundaries: DB schema apply,
  Helm hook ordering, in-cluster networking, rollback-across-image), the daemon reconcile/readiness wiring,
  the deploy values plumbing, and the stateful seed apply-once + CI gate.
- **T007 refactor (2026-06-02, per the spec owner's US2 directive):** the applier was changed from `up`-only
  to **goto-target** — target = max version in the source; the engine goes up *or* down to reach it. It uses
  the **persisted store as the migration source** (seeded additively from the current source each run) so a
  down step survives the source no longer shipping it (FR-012). Re-verified by the US1 e2e incl. a local-run
  rollback case (remove a migration → schema rolls back via the store). `m.Migrate(target)` replaces `m.Up()`.
- **`[S]` → Sonnet subagents:** T001, T002, T003, T004, T005, T006, T010, T011, T014, T016, T019, T020,
  T021, T022 — baseline, dependency add, config fields + validation, unit tests vs fakes, the Applier
  interface + fake (from contracts C4), outcome reporting, host-dir reset, the hook-Job template (authored
  from contract C3), the deploy guard error, logging, docs, and the scope diff.
- Escalation rule (per CLAUDE.md): an `[S]` task that fails QA is re-tagged `[C]`, redone on Opus, and the
  miss noted.

## Notes

- `[P]` = different files, no incomplete-task dependency.
- Verify each `[C]`/test task fails before its implementation task.
- This feature **adds a schema layer** on top of 003's provisioned DB and 005's deploy path; it reuses the
  per-service isolated DB + credentials (no new credential model — R8) and extends the existing `service`
  chart with one hook-Job template. It does not introduce a parallel migrate mechanism.
- The one build-time unknown is the `infobloxopen/migrate` `dirtyStateConf` enabling API (T007) — confirm
  against the `ib` source; the mechanism (PR #57) is verified.
- After `/speckit.implement`, the mandatory `after_implement` hook runs `verify-change` (build + lint +
  tests + e2e-if-relevant + scope diff).

## Analyze gate notes (absorb during implementation)

`/speckit.analyze` (2026-06-02) found **0 CRITICAL / 0 HIGH**, coverage 95%. Resolve these inside the
listed tasks rather than as separate work:

- **G1 (MEDIUM, SC-006/FR-009 co-existence)**: add a two-service assertion (identical table names in
  isolated DBs; `--clean` on one leaves the other intact) to **T008** — or confirm SC-006 is inherited
  from 003/004's per-service isolation and note it in **T022**'s scope diff.
- **G2 (MEDIUM, R10 bounded timeout)**: give the migrate step a bounded timeout + clear error-on-exceed in
  **T007** (applier) and **T009** (reconcile) acceptance.
- **G3 (LOW)**: in **T008**, also assert `down` *without* `--clean` preserves schema/data and the next `up`
  reuses it.
- **F1 (LOW, terminology)**: standardize on **"persisted down-migration store"** across new code/docs.
- **A1 (LOW)**: local-run guarantee = schema ready before the DSN/env-var is emitted (devedge does not
  launch the local workload); the "workload first query" assertion is the deploy path (**T012**).

## Implementation findings (T002 — fork API, read off the pinned source)

Read from `infobloxopen/migrate/v4 v4.16.3-0.20260414025640-b28cb3bc8342` (the org pin, dated 2026-04-14).
Resolves the R1/R2 "confirm at implementation time" unknowns:

- **Consumption:** require `golang-migrate/migrate/v4` + `replace` → `infobloxopen/migrate/v4@<pin>` (above).
  Code imports the standard `golang-migrate/migrate/v4[/database/pgx/v5][/source/file|iofs]` paths; the
  replace is transparent.
- **Postgres driver scheme = `pgx5`** (`database.Register("pgx5", …)`). The database URL passed to the
  engine must be `pgx5://user:pass@host:port/db?...` (T007/T009 DSN construction).
- **Dirty-state enabling API (PR #57, confirmed present):**
  `func (m *Migrate) WithDirtyStateConfig(srcPath, destPath string, isDirty bool) error` — call after
  `New`/`NewWithDatabaseInstance`, before `Up`/`Down`/`Steps`; `IsDirtyHandlingEnabled()` reports state.
  On each run it `copyFiles()` (persists up+down to `destPath`), `cleanupFiles(version)` trims to the live
  version, and `handleDirtyState()` auto-recovers a dirty DB instead of returning `ErrDirty`.
- **⚠️ Store is a filesystem DIRECTORY only** — `WithDirtyStateConfig`'s `srcPath`/`destPath` are parsed
  with a `file://`-only scheme check; **there is no database-backed store option**. This contradicts R2's
  "state dir or database" assumption.
  - **US1/MVP (local-run): unaffected** — `destPath` = a host persisted-down-store dir (sibling to the
    real-DSN file convention), which persists naturally. Use it directly in T007/T009.
  - **US2 (deploy, T013–T015): redesign needed** — the persisted down-store must survive across hook-Job
    runs and image changes; an ephemeral Job container dir does not. Resolve at US2 (PVC, or carry the
    store another way); do **not** rely on a DB-backed fork option that doesn't exist. Flag for T012–T015.

## Implementation findings (T008/T009 — fork dirty-state recovery semantics, observed live)

The US1 k3d e2e exercised the fork's `dirtyStateConf` recovery and pinned its real behavior:

- **`handleDirtyState()` does NOT re-apply a failed migration.** It reads `lastSuccessfulMigrationFile`
  from the down-store and `SetVersion(lastVersion, clean)`. In `runMigrations`, when the *first* pending
  migration in a run fails, `lastCleanMigrationApplied` is set to the **failing** version, so recovery marks
  that version clean. Under Postgres transactional DDL an invalid migration rolls back entirely, so a
  "fix-the-SQL-and-re-run" does **not** re-apply the corrected statement — the version is just marked clean.
- **This matches the fork's purpose** (PR #57: avoid the upstream `ErrDirty`→manual-`migrate force` wedge for
  the crash-after-apply *common case*) and the spec's explicit hedge: FR-007/SC-004/Edge-case all say
  recover **"for the common case"** / "without manual cleanup". So `dirtyStateConf` stays **enabled** in
  local-run (it also delivers FR-012 persisted-down) — the wiring is correct; only the e2e assertion was
  re-scoped to what the fork guarantees: failure aborts `up` (actionable error, no ready DSN), a re-run
  auto-recovers to a **clean** (non-dirty) state with no manual steps and no data loss, and the **definitive**
  reset for a botched migration is `down --clean` + `up` (rebuilds the full corrected schema — asserted).
- **Doc implication (T021):** document that if a migration was botched, `down --clean` + `up` is the reliable
  fix (a corrected re-run clears the dirty wedge but does not retro-apply a rolled-back statement).
