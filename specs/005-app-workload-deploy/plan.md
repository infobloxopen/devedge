# Implementation Plan: Deploy the app workload onto the resolved cluster

**Branch**: `005-app-workload-deploy` | **Date**: 2026-06-02 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/005-app-workload-deploy/spec.md`

## Summary

Add an **opt-in workload layer** so `de project up --deploy` runs the service *inside* the resolved
cluster (004), next to the dependencies it already provisions there (003) — with no manual
`kubectl`/`helm`. The existing `service` Helm chart (today emitted only by `de project chart`) becomes
the deploy artifact: it gains an **Ingress** so the service is reachable over its dev hostname through
devedge's existing external-dns/ingress-watch path, and its dependency wiring is pointed at the
**in-cluster Service DNS** (not the host port-forward used for local-run). Images come from a **declared
reference by default**, and are **built from the project and loaded into the cluster (`k3d image import`)
when a build is declared**. Local-run stays the default inner loop; deploy is opt-in and a service's dev
hostname resolves to exactly one running instance at a time.

## Technical Context

**Language/Version**: Go 1.23 (existing devedge module)
**Primary Dependencies**: `internal/cluster` (004 — resolved `ClusterTarget` + `EnsureCluster`),
`internal/depruntime` (003 — per-service bindings + the in-cluster shared instances), `internal/helm`
(embedded `service` chart + `Install`/`WriteChart`), `internal/daemon` + `internal/client` (route
registration; per-service binding creds), `pkg/config` (`ServiceConfig`). External CLIs already
required: `helm`, `kubectl`, `k3d`, container runtime (`docker`); the build path adds `docker build` +
`k3d image import`.
**Storage**: workload state is k8s (a Helm release per service); per-service dependency data persists in
003's PVCs (unchanged). No new host state beyond what 003/004 already write.
**Testing**: `go test` (unit: config decode, image-source/opt-in resolution, dependency-DSN rewrite) +
k3d e2e (`test/e2e/`, Principle III): deploy reaches Ready, connects to its dependency in-cluster, is
reachable over its dev hostname, idempotent redeploy, down removes only its workload, two-service
coexistence.
**Target Platform**: developer macOS/Linux + Linux CI; local k3d clusters only.
**Project Type**: single Go project — CLI (`de`) + background daemon (`devedged`) as a local control plane.
**Performance Goals**: reference-image deploy (no build) bounded by `helm upgrade --install --wait` +
readiness; build path additionally bounded by `docker build` + `k3d image import`; redeploy fast-path
(no image change) is a rollout no-op.
**Constraints**: idempotent + re-runnable (Principle I); opt-in only — default `de project up` behavior
for non-`--deploy` runs is unchanged (FR-010); never leave a half-deployed workload (FR-007); never
mutate the user's global kube context (reuse 004's explicit-context discipline); deterministic,
per-service-unique naming so co-located deployed services never collide (FR-008).
**Scale/Scope**: many deployed services co-located on the shared dev cluster; engines remain
postgres + redis (003).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Assessment | Evidence / obligation |
|-----------|------------|-----------------------|
| **I. Edge-First Developer Experience** | **PASS** — extends it | One opt-in command runs the service in-cluster, reachable by its stable dev hostname; deploy + down are idempotent and safe to repeat (FR-005/006). Default (non-`--deploy`) UX is unchanged. |
| **II. Spec-Driven, Test-Driven Delivery** | **PASS** | Spec + clarifications complete; tasks put test work before code; resolution/DSN-rewrite logic is pure-ish Go (unit-tested), behavior proven by k3d e2e. |
| **III. End-to-End Confidence Over Mocked Comfort** | **PASS** — central | New k3d e2e exercises the real deploy: workload Ready, in-cluster dependency connect, reachable over the dev hostname, redeploy, down, coexistence. Routing + cluster integration change → e2e impact assessed. |
| **IV. Portable Core, Explicit Platform Adapters** | **PASS** | Deploy *orchestration* (resolve image source → build?/load? → render+install → wait → route) is portable core in `internal/deploy`; `docker build` and `k3d image import` sit behind an explicit `ImageBuilder` adapter; cluster ops stay behind 004's `cluster.Provider`/`EnsureCluster` and `helm`. |
| **V. Safe Reconciliation and Observable Operations** | **PASS** | `helm upgrade --install --wait` is the deterministic converge; reports cluster + workload status (FR-009); failures are actionable with no half-deploy (FR-007); explicit-context only; structured logs on build/deploy/teardown. |

**No violations.** Complexity Tracking is empty.

Two **scoped notes** (not violations):
1. The deployed workload reaches its dependency over the **in-cluster Service DNS** with 003's
   per-service credentials, which differs from local-run's host port-forward DSN. This needs a small,
   explicit in-cluster connection-secret step (see research D4) — it does **not** change 003's default
   contract, it adds an in-cluster realization of the same binding.
2. The `service` chart already exists; this feature **extends** it (Ingress + in-cluster dep wiring)
   rather than introducing a parallel deploy mechanism.

## Project Structure

### Documentation (this feature)

```text
specs/005-app-workload-deploy/
├── plan.md              # This file
├── research.md          # Phase 0 — decisions & rationale
├── data-model.md        # Phase 1 — config additions + entities
├── quickstart.md        # Phase 1 — deploy usage (dev + CI)
├── contracts/
│   └── cli-and-chart.md  # Phase 1 — CLI deltas + service-chart values contract
├── checklists/
│   └── requirements.md   # Spec quality checklist (from /speckit.specify)
└── tasks.md             # Phase 2 — created by /speckit.tasks
```

### Source Code (repository root)

```text
pkg/config/service.go         # CHANGE: ServiceSpec gains a `workload` block (image | build, port, replicas)
internal/deploy/              # NEW: portable deploy orchestration behind adapters
├── deploy.go        # Deployer: resolve image (ref|build) -> ensure image in cluster -> render+install
│                    #           service chart with in-cluster dep wiring -> wait Ready
├── deploy_test.go   # NEW: unit tests (image-source resolution, in-cluster DSN rewrite) against fakes
├── image.go         # ImageBuilder adapter iface + docker/k3d impl (build + `k3d image import`)
└── dsn.go           # in-cluster DSN derivation (host-forward DSN -> in-cluster Service DNS)

internal/helm/charts/service/templates/
├── deployment.yaml  # CHANGE: dep env points at the in-cluster DSN secret (already modeled)
└── ingress.yaml     # NEW: Ingress (devedge.io/expose) so the dev hostname routes to the workload

cmd/de/
├── main.go          # CHANGE: `de project up --deploy` opt-in -> deploy after ensure+deps;
│                    #         `de project down` removes the service's workload release
└── deploy.go        # NEW: CLI glue (build the Deployer from the resolved target + config, report status)

internal/daemon/ + internal/client/   # CHANGE (small): expose the per-(service,dep) in-cluster
│                                       #   connection so the workload's DSN secret can be populated (D4)
test/e2e/
└── workload_deploy_test.go   # NEW: deploy Ready + in-cluster dep connect + hostname reach + redeploy + down + coexistence
```

**Structure Decision**: Single Go project. Deploy orchestration is a new portable-core package
(`internal/deploy`) that depends on the 004 `ClusterTarget` and the `service` chart; image build/load is
an explicit adapter (Principle IV). The CLI (`cmd/de`) stays the orchestrator that resolves the target
(004), provisions deps (003), then drives the Deployer. The `service` chart is extended, not replaced.

## Complexity Tracking

> No constitutional violations — section intentionally empty.
