# Tasks: Cluster topology — shared dev cluster, ephemeral CI clusters, co-existence-safe projects

**Input**: Design documents from `/specs/004-cluster-topology/`
**Prerequisites**: plan.md ✓, spec.md ✓, research.md ✓, data-model.md ✓, contracts/ ✓

**Tests**: INCLUDED — the devedge constitution mandates test-first (Principle II) and k3d e2e for
boundary changes (Principle III), and the spec/contracts define explicit contract tests (CT-1…CT-7).

## Format: `[ID] [P?] [Story?] [S|C] Description with file path`

- **[P]**: parallelizable (different files, no dependency on an incomplete task)
- **[Story]**: `[US1]`…`[US4]` (user-story phases only)
- **[S] / [C]**: hub model-routing tag — `[S]` simple/mechanical → Sonnet subagent; `[C]` complex → Opus.
  Every task carries one (the `before_implement` / `route-tasks` gate requires it).

---

## Phase 1: Setup

- [X] T001 [S] Record the pre-change baseline — run `make build`, `make lint`, `go test ./...` on branch `004-cluster-topology` and capture results in the PR description (baseline for the verify/scope gate). **Baseline @ `17b80f1`: build OK, `go vet` clean, all packages pass (`go test ./...` green).**

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: topology core + daemon per-context plumbing that ALL user stories depend on.

**⚠️ No user-story work begins until this phase is complete.**

### Tests first

- [X] T002 [P] [S] Unit tests for `DetectEnvironment` precedence (override `DEVEDGE_ENV`/flag > truthy `CI` > `Dev`) and `Resolve`→`ClusterTarget` (dev→`devedge`, ephemeral→`devedge-ci-<runid>`, dedicated→`devedge-proj-<slug>`; deterministic + collision-free names) in `internal/cluster/topology_test.go` (CT-1, CT-2) — must fail first.
- [X] T003 [P] [S] Unit test back-compat: daemon `ApplyDependencies` with empty `KubeContext` provisions against the current context exactly as today, using the in-memory provisioner fake, in `internal/daemon/server_test.go` (CT-3) — must fail/compile-fail first.
- [X] T006 [P] [S] Tests for `EnsureCluster` against a fake `Provider` in `internal/cluster/ensure_test.go`: reuse-if-present (no second create), create-once-under-host-lock, present-but-unbootstrapped → reconcile (re-bootstrap), missing tool / failed create → actionable retryable error with no half-created leftover (FR-011, FR-012) — must fail first.

### Implementation

- [X] T004 [S] Implement `Environment` enum + `DetectEnvironment()` in `internal/cluster/topology.go` (override > `CI` > `Dev`).
- [X] T005 [C] Implement `ClusterTarget` + `Resolve(project string, dedicated bool)` in `internal/cluster/topology.go` (dev/ephemeral/dedicated naming + fields per data-model.md) — depends on T004.
- [X] T007 [C] Implement `EnsureCluster`, `EnsureEphemeral`, and `Teardown` in `internal/cluster/ensure.go`: idempotent reuse, host-level `flock` on `~/.devedge/cluster-<name>.lock`, create + `Bootstrap` if absent, reconcile if unbootstrapped, explicit-context only, actionable errors + no half-state (FR-002, FR-007, FR-011, FR-012) — depends on T005, T006.
- [X] T008 [C] Extend the daemon dependency API + `DepManager` to accept a per-request `Target{KubeContext, Namespace}` and route to a lazily-built per-context `HelmProvisioner` (`map[string]*HelmProvisioner`, all `Close()`d on shutdown); empty context preserves today's behavior, in `internal/daemon/` (+ `internal/depruntime` if needed) — depends on T003.
- [X] T009 [S] Add `KubeContext` + `Namespace` (default `devedge-deps`) to the `ApplyDependencies` request in `internal/client/` and the daemon request type — depends on T008 (turns CT-3 green).

**Checkpoint**: resolution + ensure + per-context provisioning exist and are unit-green.

---

## Phase 3: User Story 1 — Project up resolves & ensures the shared dev cluster (Priority: P1) 🎯 MVP

**Goal**: `de project up` auto-resolves + ensures the shared `devedge` cluster and lands the project on it, with no manual cluster/context steps.

**Independent Test**: on a clean machine, `de project up` for a project with one dependency → cluster ensured once, project up on it, no context switch; a second project lands on the same cluster.

- [X] T010 [C] [US1] e2e (k3d) in `test/e2e/cluster_topology_test.go` (CT-4 / C1–C6): clean machine → `de project up` ensures `devedge` once + lands deps + prints `cluster: devedge (shared dev)` + leaves current context unchanged; second `up` reuses (no 2nd cluster); idempotent re-up; no-deps project still resolves + registers routes; assert the reuse path does **not** re-create the cluster (fast-path) — must fail first.
- [X] T011 [C] [US1] Wire `de project up` in `cmd/de/main.go`: `DetectEnvironment` → `Resolve` → `EnsureCluster` → print `cluster: <name> (<mode>)` → pass target to provisioning + route registration; actionable non-zero exit on ensure failure (FR-001/002/003/011/012/013) — depends on T005, T007.
- [X] T012 [S] [US1] Thread the resolved `Target{KubeContext, Namespace}` from project-up into `provisionDependencies` / `client.ApplyDependencies` in `cmd/de/dependencies.go` — depends on T009, T011.

**Checkpoint**: MVP — shared-cluster auto-ensure + targeting works end-to-end.

---

## Phase 4: User Story 2 — Many projects coexist without interfering (Priority: P1)

**Goal**: two projects co-located on `devedge` are per-service isolated and non-interfering.

**Independent Test**: two projects with dep name `db` up at once → each isolated store; down one, other healthy.

- [X] T013 [C] [US2] e2e (k3d) in `test/e2e/cluster_topology_test.go` (CT-5 / SC-002): two projects with identical dep name `db` up simultaneously on `devedge` → per-service isolated stores, neither reads the other; `down` one leaves the other healthy — must fail first.
- [X] T014 [S] [US2] Add a project/service-slug unique-naming helper for any cluster resource this feature introduces (dedicated-instance release names, namespace labels) in `internal/cluster/topology.go`, and assert 003's `(service,dependency)` slug isolation is preserved (no regression).
- [X] T015 [S] [US2] Ensure `de project down` releases only the requesting project's footprint on the *resolved* cluster (never the shared cluster or another project) in `cmd/de/main.go` (FR-005).

**Checkpoint**: co-existence verified on the shared cluster.

---

## Phase 5: User Story 3 — CI gets a dedicated ephemeral cluster automatically (Priority: P2)

**Goal**: `de ci run` creates a per-run ephemeral cluster and tears it down on every exit; the same e2e suite passes on both topologies.

**Independent Test**: `de ci run -- <cmd>` creates + tears down (pass and fail); existing dep suite passes on dedicated CI cluster and shared `devedge` unchanged.

- [X] T016 [C] [US3] e2e in `test/e2e/ci_ephemeral_test.go` (CT-6 / FR-007/008): `de ci run -- <cmd>` creates `devedge-ci-<runid>`, runs the cmd, tears down on success AND on induced failure within a bounded time, leaves no leftover; concurrent invocations isolated — must fail first.
- [X] T017 [C] [US3] e2e (CT-7 / FR-014/SC-004): run the existing dependency e2e suite against a dedicated CI cluster and against shared `devedge` with no test changes, in `test/e2e/` — must fail first.
- [X] T018 [C] [US3] Implement `de ci run -- <command...>` in `cmd/de/ci.go`: force `Environment=Ephemeral`, `EnsureEphemeral`, run the wrapped command with a scoped context (no global-context mutation), deferred + signal-trapped `Teardown`, propagate the wrapped exit code (FR-007/008/009) — depends on T007.
- [X] T019 [S] [US3] Register `ciCmd()` in `cmd/de/main.go` — depends on T018.

**Checkpoint**: CI ephemeral lifecycle + cross-topology suite green.

---

## Phase 6: User Story 4 — Explicit dedicated isolation opt-in (Priority: P3)

**Goal**: a project can opt into a dedicated cluster; the rare per-dependency dedicated-instance is also available.

**Independent Test**: `cluster.dedicated: true` project lands on its own cluster while a co-located default project lands on `devedge`.

- [X] T020 [S] [US4] Unit tests: `spec.cluster.dedicated` strict-decode + `Resolve` routing (dedicated→`devedge-proj-<slug>`, default→`devedge`) in `pkg/config/service_test.go` + `internal/cluster/topology_test.go` — must fail first.
- [X] T021 [C] [US4] e2e in `test/e2e/cluster_topology_test.go`: a `cluster.dedicated: true` project lands on its own cluster while a co-located non-opted project lands on `devedge`; destructive down removes the dedicated cluster only (FR-010 / US4) — must fail first.
- [X] T022 [S] [US4] Add optional `spec.cluster.dedicated` (bool, default false) to `ServiceConfig` with strict decode + validation in `pkg/config/service.go`.
- [X] T023 [S] [US4] Extend `Resolve` to honor `cluster.dedicated` (→ `devedge-proj-<slug>`, `Dedicated=true`) in `internal/cluster/topology.go` — depends on T022, T005.
- [X] T024 [S] [US4] Support destructive down for a dedicated-cluster project (remove its dedicated cluster via `DeleteAndCleanup`, never the shared one) in `cmd/de/main.go`.
- [X] T025 [S] [US4] Add optional `dependencies[].dedicated` (bool, default false) to `ServiceConfig` with strict decode + validation in `pkg/config/service.go` (FR-016, rare exception).
- [X] T026 [C] [US4] Provision a per-service **dedicated instance** (own Helm release, service-slug-named) when `dependencies[].dedicated` is set, instead of attaching to the shared per-engine instance, in `internal/depruntime/` — depends on T025, T030; keep minimal and scope-review at the QA gate.
- [X] T030 [C] [US4] **(remediation — Principle II / G2)** Test-first (e2e/integration) for dedicated-instance provisioning in `test/e2e/cluster_topology_test.go`: a service with `dependencies[].dedicated: true` gets its **own** Helm release isolated from the shared per-engine instance, while a co-located default service still attaches to the shared instance. **Author before T026** — must fail first.
- [X] T031 [C] [US4] **(remediation — FR-015 / G1)** Test-first e2e for opt-in toggle migration in `test/e2e/cluster_topology_test.go`: a project up on the shared cluster, then reconfigured `cluster.dedicated: true`, on the next `de project up` is **released from the shared cluster** and comes up on its dedicated cluster — never running in two places — must fail first.
- [X] T032 [C] [US4] **(remediation — FR-015 / G1)** Implement toggle migration in `cmd/de/main.go` (project-up flow): detect that the resolved target changed for an already-up project and release the prior cluster's footprint (routes + dependency bindings) before provisioning on the newly resolved cluster — depends on T031, T023, T024.

**Checkpoint**: all user stories independently functional.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [X] T027 [P] [S] Docs: update devedge `README.md` / `CLAUDE.md` / `CHANGELOG.md` for the topology model + `de ci run`, and validate every step in `specs/004-cluster-topology/quickstart.md`.
- [X] T028 [P] [S] Add structured logging on resolve / ensure / teardown across `internal/cluster` (Principle V observability — report cluster placement + lifecycle transitions).
- [X] T029 [S] Final scope diff vs spec FR-001…FR-017 / SC-001…SC-008; explicitly document that **FR-017 (shared-resource declaration) is intentionally not built** (resources are private by default; no shareable resource type exists yet — see research.md D9), so the omission is a recorded decision, not a gap.

---

## Dependencies & Execution Order

- **Setup (P1)** → no deps.
- **Foundational (P2)** → after Setup; **blocks all user stories**. Internal order: T002/T003/T006 (tests, parallel) → T004 → T005 → T007; T008 → T009.
- **US1 (P3)** → after Foundational. T010 (test) → T011 → T012. **MVP.**
- **US2 (P4)**, **US3 (P5)**, **US4 (P6)** → each after Foundational; independently testable. US1 should land first (shared resolution is the common path the others exercise).
- Within US4: T030 (dedicated-instance test) precedes T026; T031 (toggle-migration test) precedes T032 (FR-015 impl), which depends on T023 + T024.
- **Polish (P7)** → after the targeted stories.

### Parallel opportunities

- Foundational tests T002 / T003 / T006 run in parallel (distinct files).
- After Foundational, US2 / US3 / US4 can be staffed in parallel; within them tests precede impl.
- Polish T027 / T028 run in parallel.

## Implementation Strategy

- **MVP** = Setup + Foundational + **US1** → stop & validate (shared-cluster auto-ensure + targeting).
- Then US2 (coexistence), US3 (CI ephemeral), US4 (dedicated opt-in) as incremental, independently
  testable slices.
- TDD throughout: the `[C]` e2e/test tasks (T002/T003/T006/T010/T013/T016/T017/T020/T021) are written
  to fail first.

## Model Routing (hub gate)

- **`[C]` → Opus (14):** T005, T007, T008, T010, T011, T013, T016, T017, T018, T021, T026, T030,
  T031, T032 — topology resolution policy, ensure/teardown (concurrency + lifecycle), daemon
  per-context provisioning, CLI orchestration, the `de ci run` wrapper (signals/defer),
  dedicated-instance provisioning, the FR-015 toggle migration, and all k3d e2e (real boundaries).
- **`[S]` → Sonnet subagents (18):** T001, T002, T003, T004, T006, T009, T012, T014, T015, T019,
  T020, T022, T023, T024, T025, T027, T028, T029 — config fields, request-envelope wiring, unit
  tests, registration, docs, logging.
- Escalation rule (per CLAUDE.md): an `[S]` task that fails QA is re-tagged `[C]`, redone on Opus,
  and the miss noted.

## Notes

- **Bootstrap now installs cert-manager (decision 2026-06-02).** `EnsureCluster`'s bootstrap is a
  **hard error** on failure (ensure must yield a fully bootstrapped cluster). Because the
  `ClusterIssuer` requires cert-manager's CRDs+webhook, `cluster.Bootstrap` now installs cert-manager
  (`certManagerVersion`, pinned) and waits for it on the **local** cluster — safe because `Bootstrap`
  gates on `ValidateLocalCluster` first (a real/remote cluster is refused, never auto-installed into).
  Ensure-created clusters pass `--kubeconfig-switch-context=false` so they never hijack the user's
  current context (FR-013/D8). Traces to FR-002; recorded for the T029 scope diff.
- `[P]` = different files, no incomplete-task dependency.
- Verify each `[C]`/test task fails before its implementation task.
- The `internal/cluster` ↔ `internal/k3d` duplication is **not** touched (out of scope per plan).
- After `/speckit.implement`, the mandatory `after_implement` hook runs `verify-change` (build + lint
  + tests + e2e-if-relevant + scope diff).
