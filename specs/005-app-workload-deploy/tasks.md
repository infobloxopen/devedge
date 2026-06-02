# Tasks: Deploy the app workload onto the resolved cluster

**Input**: Design documents from `/specs/005-app-workload-deploy/`
**Prerequisites**: plan.md ✓, spec.md ✓, research.md ✓, data-model.md ✓, contracts/ ✓

**Tests**: INCLUDED — the devedge constitution mandates test-first (Principle II) and k3d e2e for
boundary changes (Principle III). Deploy touches real boundaries (image build/load, helm install,
in-cluster networking, ingress routing), so e2e coverage is central.

## Format: `[ID] [P?] [Story?] [S|C] Description with file path`

- **[P]**: parallelizable (different files, no dependency on an incomplete task)
- **[Story]**: `[US1]`…`[US3]` (user-story phases only)
- **[S] / [C]**: hub model-routing tag — `[S]` simple/mechanical → Sonnet subagent; `[C]` complex → Opus.
  Every task carries one (the `before_implement` / `route-tasks` gate requires it).

---

## Phase 1: Setup

- [X] T001 [S] Record the pre-change baseline — `make build`, `make lint`, `go test ./...` on branch `005-app-workload-deploy`; capture in the PR description (baseline for the verify/scope gate).

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: config surface + deploy core + image adapter + in-cluster dependency wiring + chart Ingress that ALL user stories depend on.

**⚠️ No user-story work begins until this phase is complete.**

### Tests first

- [X] T002 [P] [S] Unit tests for `spec.workload` strict-decode + validation (image XOR build, `port` required 1–65535, `replicas` default 1, build requires `context`) in `pkg/config/service_test.go` — must fail first.
- [X] T003 [P] [S] Unit tests for image-source resolution (reference → as-is; build → derived tag) and in-cluster DSN derivation (003 binding + in-cluster Service host → DSN) against fakes in `internal/deploy/deploy_test.go` — must fail/compile-fail first.

### Implementation

- [X] T004 [S] Add the `spec.workload` block (`Image` | `Build{Context,Dockerfile}`, `Port`, `Replicas`) to `ServiceConfig` with strict decode + validation in `pkg/config/service.go`; expose a `Workload()` accessor (and a `WorkloadDeclarer` interface in `pkg/config/resource.go`).
- [X] T005 [C] Implement `internal/deploy` `Deployer` + `ImageBuilder` adapter interface in `internal/deploy/deploy.go`: resolve image source → ensure image present in cluster (via builder) → render + `helm upgrade --install --wait` the `service` chart with image/port/replicas/hostname/dep-env → wait Ready; actionable errors, no half-deploy (FR-002/005/007) — depends on T003.
- [X] T006 [C] Implement the docker/k3d `ImageBuilder` in `internal/deploy/image.go`: reference → no-op; build → `docker build -t <tag> <context>` then `k3d image import <tag> -c <cluster>` (no registry) — depends on T005.
- [ ] T007 [S] Implement in-cluster DSN derivation (003 `(db,user,password)` binding + in-cluster Service host `devedge-<engine>.<ns>.svc.cluster.local:<port>` → real DSN) in `internal/deploy/dsn.go` — depends on T003.
- [X] T008 [S] Add an `ingress.yaml` to the `service` chart (host `service.hostname`, annotated `devedge.io/expose=true`, backend → the Service) and thread `service.hostname`; confirm `deployment.yaml` dep-env references the `<svc>-<dep>-dsn` Secret, in `internal/helm/charts/service/templates/`.
- [ ] T009 [C] Extend deploy-time dependency provisioning to emit the in-cluster `<service>-<dep>-dsn` Secret (key `dsn`) into the resolved cluster from the daemon's binding creds (request carries a "deploy" flag; local-run unchanged), in `internal/daemon/` + `internal/client/` — depends on T007.

**Checkpoint**: config + deploy core + image adapter + in-cluster dep secret + chart Ingress exist and are unit-green.

---

## Phase 3: User Story 1 — Deploy the service into the resolved cluster (Priority: P1) 🎯 MVP

**Goal**: `de project up --deploy` runs the service in the resolved cluster with no manual k8s steps; `down` removes it; redeploy is idempotent.

**Independent Test**: declare a reference-image workload + a dependency; `up --deploy` → workload Ready in the resolved cluster; re-run → no duplicate; `down` → workload gone.

- [X] T010 [C] [US1] e2e (k3d) in `test/e2e/workload_deploy_test.go`: `up --deploy` of a reference-image workload deploys onto the resolved cluster and reaches Ready; idempotent re-deploy (rollout, no duplicate); `down` removes the release; failure (bad image) is actionable with no half-deploy (FR-002/005/007) — must fail first.
- [X] T011 [C] [US1] Wire `de project up --deploy` in `cmd/de/main.go` + `cmd/de/deploy.go`: opt-in flag → after ensure (004) + deps (003) build the `Deployer` from the resolved target + `spec.workload`, deploy, and report `deployed: <svc> -> cluster <name> (<n> replicas), https://<hostname>` (FR-002/009) — depends on T005, T006, T008, T009.
- [X] T012 [S] [US1] `--deploy` with no `spec.workload` exits non-zero with an actionable message in `cmd/de/deploy.go` (data-model validation) — depends on T011.

**Checkpoint**: MVP — opt-in in-cluster deploy + idempotent redeploy + down works end-to-end.

---

## Phase 4: User Story 2 — Deployed service reaches deps & is addressable (Priority: P1)

**Goal**: the in-cluster workload connects to its dependencies and is reachable over its dev hostname.

**Independent Test**: deploy a service with a Postgres dependency; from the workload connect to the DB over the injected in-cluster DSN and round-trip a row; resolve the dev hostname → workload responds.

- [ ] T013 [C] [US2] e2e in `test/e2e/workload_deploy_test.go`: the deployed workload connects to its Postgres dependency over the in-cluster `<svc>-<dep>-dsn` Secret (write+read a row), and the service's dev hostname routes to the workload via the ingress path (FR-003/004) — must fail first.
- [ ] T014 [C] [US2] e2e: a `workload.build` service is built (`docker build`) + `k3d image import`ed + deployed + reaches Ready on the resolved cluster (FR-011 build path) — must fail first.

**Checkpoint**: deployed service is data-connected and addressable; both image sources proven.

---

## Phase 5: User Story 3 — Many deployed services coexist (Priority: P2)

**Goal**: co-located deployed services on the shared cluster are isolated and non-interfering.

**Independent Test**: deploy two services with identical internal names to the shared cluster; both Ready + addressable; `down` one leaves the other Ready.

- [ ] T015 [C] [US3] e2e in `test/e2e/workload_deploy_test.go`: two services deployed to the shared cluster each get an isolated release + ingress host; both Ready; `down` one leaves the other Ready + addressable (FR-008/006) — must fail first.
- [ ] T016 [S] [US3] Per-service release/ingress naming via `cluster.ProjectSlug` (collision-free for distinct services) in `internal/deploy` + the chart values; assert no regression to 003/004 isolation.

---

## Phase 6: Teardown & Polish

- [X] T017 [C] Remove the service's workload release on `de project down` (`helm uninstall`, footprint-only — never the shared cluster or another project) in `cmd/de/main.go` (FR-006) — depends on T011.
- [ ] T018 [P] [S] Structured logging on resolve-image / build / deploy / teardown across `internal/deploy` (Principle V — report placement + workload status).
- [ ] T019 [P] [S] Docs: update devedge `README.md` / `CLAUDE.md` / `CHANGELOG.md` for `de project up --deploy` + the `spec.workload` block, and validate every step in `specs/005-app-workload-deploy/quickstart.md`.
- [ ] T020 [S] Final scope diff vs FR-001…FR-011 / SC-001…SC-005 in `specs/005-app-workload-deploy/SCOPE.md`; confirm no gold-plating and that local-run default behavior is unchanged when `--deploy` is absent.

---

## Dependencies & Execution Order

- **Setup (P1)** → no deps.
- **Foundational (P2)** → after Setup; **blocks all user stories**. Internal order: T002/T003 (tests, parallel) → T004; T005 → T006; T003 → T007 → T009; T008 parallel.
- **US1 (P3)** → after Foundational. T010 (test) → T011 → T012. **MVP.**
- **US2 (P4)**, **US3 (P5)** → after US1 (they exercise the deploy path US1 builds). Within each, tests precede impl.
- **Teardown/Polish (P6)** → after the targeted stories. T017 depends on T011.

### Parallel opportunities

- Foundational tests T002 / T003 run in parallel (distinct files). Chart work T008 parallels the Go impl.
- Polish T018 / T019 run in parallel.

## Implementation Strategy

- **MVP** = Setup + Foundational + **US1** → stop & validate (opt-in deploy + idempotent redeploy + down).
- Then US2 (deps + addressability, incl. the build path) and US3 (coexistence) as incremental slices.
- TDD throughout: `[C]` e2e/test tasks (T002/T003/T010/T013/T014/T015) are written to fail first.

## Model Routing (hub gate)

- **`[C]` → Opus (8):** T005, T006, T009, T010, T011, T013, T014, T015, T017 — deploy orchestration,
  image build/load adapter, daemon in-cluster secret emission, CLI deploy wiring, and all k3d e2e
  (real boundaries: build/load, helm install, in-cluster networking, ingress routing, coexistence).
- **`[S]` → Sonnet subagents:** T001, T002, T003, T004, T007, T008, T012, T016, T018, T019, T020 —
  config fields + validation, unit tests, DSN derivation, chart Ingress template, error wiring,
  naming helper, logging, docs, scope diff.
- Escalation rule (per CLAUDE.md): an `[S]` task that fails QA is re-tagged `[C]`, redone on Opus,
  and the miss noted.

## Notes

- `[P]` = different files, no incomplete-task dependency.
- Verify each `[C]`/test task fails before its implementation task.
- This feature **extends** the existing `service` chart (Ingress + dep wiring) and reuses 004's resolved
  target + 003's bindings — it does not introduce a parallel deploy mechanism or a new credential model.
- After `/speckit.implement`, the mandatory `after_implement` hook runs `verify-change` (build + lint +
  tests + e2e-if-relevant + scope diff).
