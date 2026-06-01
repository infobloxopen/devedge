---
description: "Task list for Dependency runtime for the Service kind"
---

# Tasks: Dependency runtime for the Service kind

**Input**: Design documents from `/specs/003-dependency-runtime/`
**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/dependency-runtime.md

**Tests**: Included — Constitution II mandates test-first (red-green-refactor); Constitution III
mandates a k3d e2e for the critical cross-boundary flow.

## Format: `[ID] [P?] [Complexity] [Story?] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks).
- **[Complexity]**: `[S]` simple/mechanical → Sonnet subagent; `[C]` complex → Opus. Per the
  agentic lifecycle in CLAUDE.md. An `[S]` task that fails the QA gate is re-tagged `[C]` and redone
  on Opus.
- **[Story]**: US1/US2/US3/US4 for user-story phases only.
- Exact file paths are included in each description.

## Path Conventions

Single Go module. Portable core in `internal/depruntime/` + `internal/dsn/`; Helm rendering in
`internal/helm/` (embedded charts + `helm` CLI); daemon in `internal/daemon/` + `internal/reconciler/`;
CLI in `cmd/de/`; tests in `test/integration/` and `test/e2e/`.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Establish the backward-compatibility baseline and tool preflight before any edits.

- [X] T001 [S] Confirm baseline is green on branch `003-dependency-runtime`: run `make build` and `make test`; record that the existing route/daemon tests and `pkg/config` tests pass unchanged (the 002 + route-API back-compat oracle).
- [X] T002 [S] Add a CLI preflight in `cmd/de/` that detects `helm`/`kubectl`/`k3d` on PATH and fails with one actionable message naming the missing tool (used by the dependency path only; never triggered for `kind: Config` or no-deps `Service`). Unit-test the message.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The dependency desired-state core, the Helm renderer, the DSN emitter, the daemon
store/endpoints, and the dependency reconciler. **Blocks all user stories.**

**⚠️ CRITICAL**: No user story work begins until this phase is complete. **DK invariant** (see
plan "Backward compatibility & external consumers"): the route registry/reconciler and the existing
`/v1/routes` + `/v1/projects` API must remain byte-compatible; new work is strictly additive.

### Tests (write first, must FAIL)

- [X] T003 [P] [S] Golden-render tests in `internal/helm/helm_test.go`: `helm template` of the embedded `postgres` and `redis` charts is deterministic and contains a StatefulSet + Service + PVC in the `devedge-deps` namespace. (skipped-with-reason if `helm` absent)
- [X] T004 [P] [S] Unit tests in `internal/dsn/dsn_test.go`: for postgres and redis, the env-var value is the indirect `fsnotify://<driver>/<abs-path>` form and the file holds the real DSN; the same shape is produced for **both** engines; the file is written `0600` atomically.
- [X] T005 [P] [C] Reconcile tests in `internal/depruntime/reconcile_test.go` against the **fake** `Provisioner`: desired→Ready state transitions; idempotent re-reconcile (no duplicate provisioning, no data loss); a bounded-timeout readiness failure surfaces per-dependency and leaves no half-state (FR-008/FR-009).
- [X] T006 [P] [S] Daemon API tests in `test/integration/dependency_api_test.go`: the **existing** `PUT /v1/routes`, `DELETE /v1/projects/{project}`, `GET /v1/status` are unchanged (path/port/shape); the new `PUT/GET/DELETE /v1/services/{svc}/dependencies` upsert/report/release and never echo raw credentials. (DK regression oracle)

### Implementation

- [X] T007 [C] Implement the embedded Helm charts in `internal/helm/charts/{postgres,redis}` (StatefulSet + headless Service + PVC, official images, `devedge-deps` namespace) and `internal/helm/embed.go` (`//go:embed`). (depends on T003)
- [X] T008 [C] Implement the `helm` CLI adapter in `internal/helm/helm.go`: `Render` (helm template), `Install` (helm upgrade --install --kubeconfig), `Uninstall`, `Lint` — via `os/exec`, deterministic, with actionable errors. (depends on T003, T007)
- [X] T009 [S] Implement `internal/dsn/dsn.go`: build the real DSN per engine, the indirect `fsnotify://<driver>/<file>` env value (uniform for postgres + redis), file-path derivation under `~/.devedge/services/<service>/`, and atomic `0600` write. (depends on T004)
- [X] T010 [C] Implement the `Provisioner` adapter in `internal/depruntime/provisioner.go`: interface (`EnsureInstance`, `EnsureDatabase`, `DropDatabase`, `Ready`) + a real impl wrapping `internal/helm` (instance) and a new `internal/cluster/exec.go` `kubectlExec` helper (psql/redis-cli for db/role/ACL), + a **fake** for tests. (depends on T005, T008)
- [X] T011 [C] Implement `internal/depruntime/desired.go` + `reconcile.go`: derive desired state from a Service's dependencies; idempotent converge (ensure instance → provision isolation → write DSN → readiness probe), per-dependency errors, bounded timeout. (depends on T005, T009, T010)
- [X] T012 [C] Implement `internal/daemon/depstore.go` (desired-dependency registry mirroring `registry.go`: event-driven, thread-safe) and wire a `internal/reconciler/dependency.go` that converges via `depruntime.Reconcile` **beside** the route reconciler without altering it. (depends on T011)
- [X] T013 [S] Add the additive daemon endpoints in `internal/daemon/api.go` (`PUT/GET/DELETE /v1/services/{svc}/dependencies`; `GET` never returns raw creds/DSN) and the matching client methods in `internal/client/client.go`. (depends on T006, T012)
- [ ] T014 [C] Add a TCP entrypoint + catch-all `HostSNI("*")` router per dependency engine (`postgres`/`redis` on `5432`/`6379` at the EdgeIP) in `internal/render/traefik.go`, plus the stable-hostname registration — **without** changing existing HTTP/TCP route rendering. (depends on T012)

**Checkpoint**: deps can be declared to the daemon, an instance can be Helm-installed, isolation
provisioned, a DSN emitted, and an engine fronted — all behind the route API, which is unchanged.

---

## Phase 3: User Story 1 - Start and reach declared dependencies (Priority: P1) 🎯 MVP

**Goal**: `de project up` on a `Service` declaring a Postgres dependency starts it, waits for
readiness, and reports a working connection (env var + real-DSN file).

**Independent Test**: Author a `Service` with one `postgres` dependency, run `de project up`,
connect with the reported DSN, create+query a table; `de project down` releases it.

### Tests (write first, must FAIL)

- [X] T015 [P] [S] [US1] Integration test in `test/integration/dependency_runtime_test.go` (fake provisioner): `up` on a Service with one postgres dep drives it to Ready, writes the DSN file + reports the `fsnotify://` env var, and `up` is idempotent. Co-existence-safe (unique names, self-cleanup, isolated daemon).
- [ ] T016 [P] [C] [US1] e2e test in `test/e2e/dependency_postgres_test.go` (k3d): Helm-install the shared Postgres, provision a service DB, connect over the reported DSN, write+read a row. **Skipped-with-reason** when Docker/k3d/helm absent (never claimed passed).
- [X] T017 [P] [S] [US1] Unit test in `pkg/config/service.go` test for the new `Dependency` helpers (default port per engine, env-var name, DSN file path).

### Implementation

- [X] T018 [S] [US1] Add `Dependency` helpers (default port, env-var name, DSN file path) in `pkg/config/service.go` — additive only; the 002 schema is frozen. (depends on T017)
- [ ] T019 [C] [US1] In `cmd/de/main.go` `projectUpCmd`, when the resource declares dependencies, send them to the daemon, wait for readiness, and print per-dependency the env var + DSN-file path + "ready" (replacing 002's "not yet supported"); per-dependency failures exit non-zero and are retryable. A no-deps `Service`/`Config` is unchanged (FR-013). (depends on T013, T011, T002)
- [ ] T020 [S] [US1] In `cmd/de/main.go` `projectDownCmd`, release the service's dependencies via the daemon (default: keep data) alongside existing route deregistration; co-located services unaffected. (depends on T013)

**Checkpoint**: a Postgres dependency is started, reachable, and reported; up/down work; route path unchanged.

---

## Phase 4: User Story 2 - Safe co-existence in the shared dev environment (Priority: P1)

**Goal**: Two services declaring identically named dependencies each get an isolated database +
credentials in the one shared instance; neither sees the other's data.

**Independent Test**: Bring up two services both declaring `postgres` dep `db`; write a distinct row
in each; confirm each sees only its own and both are reachable.

### Tests (write first, must FAIL)

- [X] T021 [P] [C] [US2] Integration test (fake provisioner) in `test/integration/dependency_runtime_test.go`: two services with dep name `db` get distinct database/role/password; cross-access is denied; one service's `down` leaves the other intact.
- [X] T022 [P] [S] [US2] Unit test in `internal/depruntime/desired_test.go`: per-(service,dependency) identifier derivation is deterministic, sanitized, and collision-avoided.

### Implementation

- [ ] T023 [C] [US2] Implement per-service isolation in the `Provisioner` (Postgres: `CREATE ROLE … LOGIN` + `CREATE DATABASE … OWNER`, idempotent; the binding names derived deterministically) so two services never collide. (depends on T010, T011)
- [ ] T024 [S] [US2] Ensure the dependency reconciler scopes all reads/writes to the requesting service and the `devedge-deps` namespace only (DK invariant: never touch other namespaces/charts). (depends on T012)

**Checkpoint**: co-existence-safe isolation proven; SC-002 holds.

---

## Phase 5: User Story 3 - Data persists across restarts, with explicit wipe (Priority: P2)

**Goal**: Data survives `down`/`up`; an explicit `--clean` drops only the requesting service's data.

**Independent Test**: up → write → down → up shows data present; `down --clean` → up shows it gone.

### Tests (write first, must FAIL)

- [X] T025 [P] [S] [US3] Integration test (fake provisioner): default `down` keeps the binding's data; `down --clean` calls `DropDatabase` for **only** that service; the shared instance/PVC are never dropped.
- [ ] T026 [P] [C] [US3] e2e (k3d) addition in `test/e2e/dependency_postgres_test.go`: write → `down` → `up` retains data; `down --clean` → `up` starts empty. Skipped-with-reason when k3d absent.

### Implementation

- [ ] T027 [S] [US3] Add `--clean` to `projectDownCmd` (default false) and plumb `clean=true` to `DELETE /v1/services/{svc}/dependencies`; default keeps data (FR-005/FR-007). (depends on T020, T013)
- [ ] T028 [S] [US3] Implement `Provisioner.DropDatabase` (Postgres `DROP DATABASE`/`DROP ROLE`; Redis `ACL DELUSER` + namespace flush) targeting only the service's binding. (depends on T010, T023)

**Checkpoint**: persistence by default; explicit, service-scoped wipe; SC-003 holds.

---

## Phase 6: User Story 4 - Deployable chart without writing Kubernetes (Priority: P3)

**Goal**: `de project chart` emits a Helm chart for the service + abstract dependency claims.

**Independent Test**: For a Service with one Postgres dep, `de project chart -o ./chart` produces a
chart that passes `helm lint` and expresses the dependency abstractly; a no-deps Service yields a
valid service-only chart.

### Tests (write first, must FAIL)

- [ ] T029 [P] [S] [US4] Golden-render + `helm lint` test in `internal/helm/helm_test.go` for the `service` chart: deterministic output; claim + secret templates present and **separable** from `deployment.yaml` (BYO-seam, plan decision 7); a no-deps Service renders a valid service-only chart. (skipped-with-reason if `helm` absent)

### Implementation

- [ ] T030 [C] [US4] Implement the embedded `internal/helm/charts/service` chart: `Chart.yaml`, `values.yaml` (service + `dependencies` list), `templates/deployment.yaml`, and **separable** `templates/dependency-claim.yaml` + `templates/dependency-secret.yaml` (abstract claim + `fsnotify://` env/secret; FR-011 + BYO seam). (depends on T007)
- [ ] T031 [S] [US4] Implement `cmd/de/chart.go` (`de project chart [-o OUTDIR]`): materialize the `service` chart from the loaded `Service`, fill the `dependencies` values, run `helm lint`; emit only (no deploy). (depends on T030, T008)

**Checkpoint**: chart emitted, lints clean, dependencies abstract; SC-004 holds.

---

## Phase 7: Slice B — Redis runtime support (Priority: P1, parity with US1–US3)

**Goal**: Bring Redis to parity with Postgres — an isolated per-service ACL user + key namespace /
logical DB index in the one shared Redis, reachable and reported via the **same** `fsnotify://` DSN
convention, with persistence + scoped wipe.

**Independent Test**: Two services each declaring a `redis` dep `cache` get distinct ACL users +
namespaces; each sees only its own keys; both reachable; `down --clean` drops only the requester's.

### Tests (write first, must FAIL)

- [ ] T032 [P] [C] [US2] Redis isolation integration test in `test/integration/dependency_runtime_test.go` (fake provisioner): two services with a `redis` dep get distinct ACL user + key namespace / logical DB index; cross-namespace access is denied; one service's `down` leaves the other intact. (mirrors T021 for Redis)
- [ ] T033 [P] [C] [US1] Redis connectivity e2e in `test/e2e/dependency_redis_test.go` (k3d): Helm-install the shared Redis, provision a service's ACL user + namespace, connect over the reported DSN, SET/GET a key, and confirm persistence across `down`/`up` and `--clean` wipe. **Skipped-with-reason** when Docker/k3d/helm absent. (mirrors T016/T026 for Redis)
- [X] T034 [P] [S] Test in `internal/depruntime/reconcile_test.go`: a recognized engine without runtime support fails **by name**, actionable and retryable (FR-012, SC-005 unsupported-engine branch).

### Implementation

- [ ] T035 [C] [US2] Implement Redis isolation in the `Provisioner` (`EnsureDatabase` for redis = `ACL SETUSER` with a per-service user + key-namespace prefix and/or logical DB index, idempotent; `Ready` = `PING`) so two services never collide, and ensure `DropDatabase` (T028) targets only the requester's Redis slice. (depends on T010, T011, T028)

**Checkpoint**: Redis at parity with Postgres across US1–US3; SC-002/SC-003 hold for both engines; FR-012 unsupported-engine path covered.

---

## Phase 8: Polish & Cross-Cutting Concerns

- [ ] T036 [P] [S] **DK regression** test in `test/integration/`: a `kind: Config` file routes via `de project up`/`down` exactly as before, and `PUT /v1/routes` + `DELETE /v1/projects/{project}` at `:15353` are unchanged — proving `platform.data.kit`'s surface is intact (see plan "Backward compatibility & external consumers").
- [X] T037 [S] **Observability (Constitution V)**: emit structured logs on each dependency provision/teardown (desired vs observed state) and assert `GET /v1/services/{svc}/dependencies` reflects per-dependency `State` without raw credentials — mirroring the existing route-mutation observability. (depends on T012, T013)
- [ ] T038 [P] [S] Update `README.md` (Service dependency runtime: `up`/`down`/`--clean`/`chart`, the `fsnotify://` DSN convention for both engines, required `helm`/`kubectl`/`k3d`) and verify `specs/003-dependency-runtime/quickstart.md` matches the shipped commands.
- [ ] T039 [S] Run the QA gate (the `after_implement` `verify-change` hook): `make build` + `make lint` + unit + integration; e2e (k3d) since this touches cluster orchestration/DNS/routing — CI provides a dedicated k3d; on a dev machine use the shared k3d if present, else report **skipped** (never claim passed); co-existence-safe. Then the scope check against this spec's acceptance criteria.

---

## Dependencies & Execution Order

- **Setup (T001–T002)** → no deps.
- **Foundational (T003–T014)** → after Setup; **blocks all stories**. Render/DSN (T007–T009) ∥; T010 needs T008; T011 needs T009+T010; T012 needs T011; T013 needs T012; T014 needs T012.
- **US1 (T015–T020)** → after Foundational. MVP. T019 needs T013+T011+T002.
- **US2 (T021–T024)** → after Foundational; builds on US1's provisioning. Independently testable.
- **US3 (T025–T028)** → after US1 (needs up/down + provisioner). Independently testable.
- **US4 (T029–T031)** → after Foundational (needs `internal/helm`); independent of US1–US3.
- **Slice B / Redis (T032–T035)** → after Foundational; T035 builds on the provisioner (T010/T011) + Postgres isolation pattern (T023) + `DropDatabase` (T028); independently testable per engine.
- **Polish (T036–T039)** → after the delivered stories; T039 (QA gate) runs last.

## Parallel Opportunities

- Foundational tests T003 ∥ T004 ∥ T005 ∥ T006 (different files); T007 ∥ T009 (charts vs dsn).
- Per-story `[P]` test tasks run together (T015∥T016∥T017; T021∥T022; T025∥T026; T032∥T033∥T034).
- Polish T036 ∥ T038.

## Model routing summary (for `/speckit.implement`)

- **`[C]` (Opus):** T005, T007, T008, T010, T011, T012, T014, T016, T019, T021, T023, T026, T030,
  T032, T033, T035 — the Helm renderer, the provisioner/reconcile core (Postgres + Redis isolation),
  daemon store/reconciler, edge-config change, the connectivity + persistence e2es, and the chart
  templates (design-bearing or boundary-touching).
- **`[S]` (Sonnet subagents):** T001–T004, T006, T009, T013, T015, T017, T018, T020, T022, T024,
  T025, T027, T028, T029, T031, T034, T036–T039 — mechanical, pattern-following work.
- Escalate any `[S]` task that fails the QA gate to `[C]`/Opus and note it.

## Implementation Strategy

- **MVP = Setup + Foundational + US1** (T001–T020): a Postgres dependency that starts, is reachable,
  and is reported via a hotload DSN. Stop and validate independently (incl. the k3d e2e) before US2+.
- **Slice order from the plan**: A (Postgres = US1+US2) → B (Redis = Phase 7, T032–T035, parity with
  US1–US3 + the FR-012 unsupported-engine path) → C (persistence/wipe = US3) → D (chart = US4). C and
  D may precede B; B is gated only on Foundational.
- US2/US3/US4 and Redis are additive increments, each independently testable; none regress US1 or the
  route path. The DK regression (T036) and QA gate (T039) guard the external-consumer invariants.
