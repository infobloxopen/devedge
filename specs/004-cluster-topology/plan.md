# Implementation Plan: Cluster topology — shared dev cluster, ephemeral CI clusters, co-existence-safe projects

**Branch**: `004-cluster-topology` | **Date**: 2026-06-01 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/004-cluster-topology/spec.md`

## Summary

devedge will resolve every project to a target Kubernetes cluster from an **explicit topology
model** instead of relying on the ambient kube context. On a developer machine, `de project up`
auto-resolves and **ensures a single shared dev cluster** (`devedge`), creating + bootstrapping it
once and reusing it thereafter; in CI a **devedge wrapper command** creates a **dedicated ephemeral
cluster per run** and tears it down on exit (even on failure). The CLI resolves the environment
(auto-detect `CI`, explicit override wins) and the cluster, then **threads the resolved kube context
+ project namespace per request** into the daemon's existing dependency provisioning — so feature
003's per-service-isolated Postgres/Redis land on the *resolved* cluster rather than whatever context
the daemon happened to start with. Co-existence rides on 003's per-service isolation (preserved, not
rebuilt); a project may opt into a **dedicated cluster** when it cannot coexist. k3d stays behind the
existing `cluster.Provider` adapter.

## Technical Context

**Language/Version**: Go 1.23 (existing devedge module)
**Primary Dependencies**: existing `internal/cluster` (k3d `Provider`, `Bootstrap`, `ValidateLocalCluster`, `PortForward`), `internal/helm` (`DefaultNamespace = "devedge-deps"`), `internal/depruntime` (`HelmProvisioner`, `Reconciler`), `internal/daemon` (HTTP API + `DepManager`), `internal/client`, `pkg/config` (`ServiceConfig`). External CLIs already required: `k3d`, `kubectl`, `helm`, container runtime (docker).
**Storage**: cluster state is k3d/Docker; per-service dependency data persists in PVCs (003, unchanged); a host-level lockfile under `~/.devedge/` guards concurrent cluster-ensure.
**Testing**: `go test` (unit, in-memory provisioner fake) + k3d-backed e2e in `test/e2e/` (constitution Principle III); the same e2e suite MUST pass on both the shared dev cluster and a dedicated CI cluster.
**Target Platform**: developer macOS/Linux + Linux CI runners; local clusters only (loopback API server, enforced by the existing `ValidateLocalCluster` safeguard).
**Project Type**: single Go project — a CLI (`de`) + a background daemon (`devedged`) acting as a local control plane.
**Performance Goals**: cluster **reuse** resolution (cluster already present) ≤ ~2s (one `k3d cluster list`); **first-time ensure** (create + bootstrap) bounded by a configurable timeout (default 300s; k3d `create --timeout 180s` + bootstrap); ephemeral **teardown** always attempted and bounded.
**Constraints**: idempotent + re-runnable (Principle I); never mutate the user's global kube-context selection — always pass `--context`/`--kube-context` explicitly (FR-013); no half-created cluster left on failure (FR-012); deterministic, project-unique naming so co-located projects never accidentally collide.
**Scale/Scope**: one shared dev cluster per host; many projects co-located on it; concurrent ephemeral CI runs each isolated. Engines remain `postgres` + `redis` (003).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Assessment | Evidence / obligation |
|-----------|------------|-----------------------|
| **I. Edge-First Developer Experience** | **PASS** — strengthens it | Auto-ensures the shared cluster with no manual `de cluster create`/context switch; ensure + down are idempotent and safe to repeat (FR-002, FR-011). |
| **II. Spec-Driven, Test-Driven Delivery** | **PASS** | Spec + clarifications complete; tasks will put test work before code; resolver/ensure logic is pure-ish Go with unit tests, behavior proven by e2e. |
| **III. End-to-End Confidence Over Mocked Comfort** | **PASS** — central | FR-014/SC-004 require the same k3d e2e suite to pass on shared **and** dedicated clusters; new e2e covers ensure/reuse, two-project co-existence, and ephemeral teardown. |
| **IV. Portable Core, Explicit Platform Adapters** | **PASS** | Topology **resolution** is portable core (`internal/cluster/topology.go`); all cluster-tool calls stay behind the existing `cluster.Provider` adapter — the resolver depends on `Provider`, not on `k3d` directly. |
| **V. Safe Reconciliation and Observable Operations** | **PASS** | Reports which cluster a project landed on (FR-003); concurrent-create guarded by a host lock + idempotent reuse (FR-011, edge case); failures are actionable and leave nothing half-created (FR-012); explicit-context only, no silent global-context mutation (FR-013); structured logs on ensure/teardown. |

**No violations.** Complexity Tracking is empty. One **pre-existing condition noted, not addressed here**: two cluster-ish packages exist (`internal/cluster` — the `Provider` home — and `internal/k3d` — used by `de cluster watch`). Topology logic goes in `internal/cluster`; **consolidating the two packages is out of scope** (it would be unrelated refactoring / gold-plating against the scope gate).

## Project Structure

### Documentation (this feature)

```text
specs/004-cluster-topology/
├── plan.md              # This file
├── research.md          # Phase 0 — decisions & rationale
├── data-model.md        # Phase 1 — entities & config additions
├── quickstart.md        # Phase 1 — dev + CI usage
├── contracts/
│   └── cli-and-api.md    # Phase 1 — CLI command + daemon API deltas
├── checklists/
│   └── requirements.md   # Spec quality checklist (from /speckit.specify)
└── tasks.md             # Phase 2 — created by /speckit.tasks (NOT here)
```

### Source Code (repository root)

```text
internal/cluster/                 # portable topology + k3d Provider adapter
├── topology.go        # NEW: Environment detection, Target resolution (project+cfg → cluster)
├── topology_test.go   # NEW: unit tests (env precedence, naming, dedicated opt-in routing)
├── ensure.go          # NEW: EnsureCluster (idempotent create+bootstrap), host-lock for races, EnsureEphemeral/Teardown
├── ensure_test.go     # NEW
├── provider.go        # (exists) Provider interface, ClusterDomain/FQDN
├── k3d.go             # (exists) K3dProvider — unchanged adapter
├── bootstrap.go       # (exists) Bootstrap / CreateAndBootstrap / DeleteAndCleanup
└── safeguard.go       # (exists) ValidateLocalCluster — reused on ensure

cmd/de/
├── main.go            # CHANGE: project up resolves+ensures cluster, passes target to daemon
├── dependencies.go    # CHANGE: pass resolved kube context + project namespace to ApplyDependencies
├── cluster.go         # (exists) explicit `de cluster ...` stays; topology reuses its helpers
└── ci.go              # NEW: `de ci run -- <cmd>` ephemeral wrapper (create→run→defer teardown)

internal/client/        # CHANGE: ApplyDependencies envelope carries Target{KubeContext, Namespace}
internal/daemon/        # CHANGE: dependency API accepts a per-request Target; DepManager resolves
│                       #         a provisioner per kube-context (map[ctx]*HelmProvisioner)
internal/depruntime/    # CHANGE: provisioner selectable per kube-context/namespace (no contract change to 003 isolation)
pkg/config/service.go   # CHANGE: ServiceSpec gains optional `cluster:` block (dedicated opt-in; + forward-looking shared markers)

test/e2e/
├── cluster_topology_test.go   # NEW: ensure+reuse, two-project co-existence, dedicated opt-in
└── ci_ephemeral_test.go       # NEW: ephemeral create+teardown (incl. on failure)
```

**Structure Decision**: Single Go project. Topology resolution + ensure are new files in
`internal/cluster` (portable core behind the `Provider` adapter, per Principle IV). The CLI
(`cmd/de`) is the orchestrator: it resolves the environment + cluster and threads the target into the
daemon; the daemon stays the provisioning executor (003 unchanged) but learns to target a
per-request kube context/namespace. A new `cmd/de/ci.go` provides the CI wrapper command.

## Complexity Tracking

> No constitutional violations — section intentionally empty.
