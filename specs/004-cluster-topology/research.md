# Phase 0 Research: Cluster topology

Decisions that resolve the open questions for the design. The Clarifications session
(2026-06-01) already settled the four highest-impact choices; this file records the supporting
engineering decisions and the one deferred non-functional item (timing targets).

## D1 — Where topology logic lives

- **Decision**: New `internal/cluster/topology.go` (environment detection + target resolution) and
  `internal/cluster/ensure.go` (idempotent create/bootstrap/teardown). They depend on the existing
  `cluster.Provider` interface, never on `k3d` directly.
- **Rationale**: `internal/cluster` already owns the `Provider` adapter, `Bootstrap`,
  `ValidateLocalCluster`, and `PortForward`. Keeping resolution there satisfies Principle IV
  (portable core, explicit adapter) and reuses the safeguard + bootstrap paths.
- **Alternatives rejected**: (a) put it in `cmd/de` — leaks policy into the CLI, untestable without
  a binary; (b) put it in `internal/k3d` — that package is the older ingress-watcher path and would
  re-entrench the k3d coupling; (c) consolidate `internal/cluster` + `internal/k3d` first —
  unrelated refactor, out of scope.

## D2 — Environment detection (FR-009)

- **Decision**: `cluster.DetectEnvironment()` precedence: (1) explicit override `DEVEDGE_ENV`
  (`dev` | `ci` | `ephemeral`) or a `--ephemeral`/`--env` flag always wins; (2) else a truthy
  standard `CI` env var → ephemeral; (3) else → shared-dev (default).
- **Rationale**: zero-config in CI (every major CI sets `CI=true`), deterministic + overridable
  locally, minimal setup (Principle I). Documented and explicit (FR-009).
- **Alternatives rejected**: explicit-only (forces every CI job to opt in — friction); detect each
  CI vendor's variables (brittle, unnecessary — `CI` is universal).

## D3 — Shared dev cluster identity (FR-002, FR-003)

- **Decision**: One well-known cluster named **`devedge`** → kube context **`k3d-devedge`**, ingress
  host port **8081** (the existing `de cluster create` default). Persistent; removed only by an
  explicit destructive command (never auto-GC'd).
- **Rationale**: a single stable name makes reuse trivial (`k3d cluster list` → present?), matches
  `dev-k3d-shared-cluster-model`, and the existing bootstrap/domain helpers already key off the
  cluster name (`ClusterDomain`, `KubeContext`).
- **Alternatives rejected**: per-user-suffixed names (no benefit on a single-user host); per-project
  shared clusters (defeats "single shared cluster").

## D4 — Ephemeral cluster identity + teardown (FR-007, FR-008)

- **Decision**: ephemeral cluster name **`devedge-ci-<runid>`** where `<runid>` = first of
  `GITHUB_RUN_ID` / `DEVEDGE_RUN_ID` / a random short token. A new **`de ci run -- <cmd>`** wrapper
  creates+bootstraps it, runs the wrapped command with the resolved context, and tears it down via a
  deferred cleanup that fires on success, failure, or signal. The Go e2e harness calls the same
  internal `EnsureEphemeral`/`Teardown` helpers directly (so tests don't shell out to the binary).
- **Rationale**: a per-run-unique name guarantees concurrent runs never collide (FR-008) and a crash
  leftover is discoverable + non-colliding (edge case); `defer`/signal-trap makes teardown
  guaranteed-on-failure (FR-007) and keeps the workflow from calling `k3d` directly (US3).
- **Alternatives rejected**: workflow-orchestrated `de cluster create/delete` (reopens "never call
  the tool directly", relies on YAML author for cleanup); daemon-reaped clusters (adds daemon
  lifecycle state for a CI-only concern — heavier than needed).

## D5 — Threading the resolved cluster into provisioning (FR-013)

- **Decision**: The **CLI resolves + ensures** the cluster, then passes a `Target{KubeContext,
  Namespace}` on the `ApplyDependencies` request. The daemon's `DepManager` keeps a
  **`map[kubeContext]*HelmProvisioner`** (lazily constructed, `Close()`d on shutdown) and routes a
  request to the provisioner for its context. The shared per-engine instances stay in
  `helm.DefaultNamespace` (`devedge-deps`) **on the resolved cluster**; 003's per-service DB/creds
  isolation is unchanged.
- **Rationale**: the CLI is the only process that sees the project file + the invocation env (incl.
  `CI`) and owns the ephemeral wrapper, so it must drive resolution; the daemon stays a provisioning
  executor. Per-context provisioners are required because `HelmProvisioner` holds context + a
  per-engine port-forward map — one global provisioner can't serve two clusters at once. This is the
  minimal change that satisfies FR-013 without re-opening 003's contract.
- **Alternatives rejected**: daemon resolves the cluster itself (it lacks the CLI's env + cwd + the
  ephemeral wrapper's lifecycle); mutate the daemon's single provisioner's context per request
  (races across concurrent projects, breaks the port-forward cache).

## D6 — Co-existence is preserved, not rebuilt (US2, FR-004, FR-006, FR-016)

- **Decision**: Default isolation = 003's per-service database/namespace + credentials within the
  shared per-engine instance, now on the resolved cluster. This feature adds **project-scoped
  naming** only for cluster resources it newly introduces, and a **dedicated-instance opt-in** routed
  through config. It does **not** add namespace-per-project for the app workload (devedge does not
  deploy the app workload today — the service runs locally and reaches deps via the hotload DSN +
  port-forward from 003).
- **Rationale**: 003 already isolates co-located services by `(service, dependency)` slug; the
  co-existence requirement is met by construction. Building a new per-project namespace/app-deploy
  path now would be speculative (no app workloads deployed yet) and fail the scope gate.
- **Alternatives rejected**: deploy each project's app into a per-project namespace now (no consumer
  — 003 doesn't deploy app workloads); validate-and-reject "cluster-global" declarations (the config
  surface can't express them — see Clarifications).

## D7 — Concurrent first-time ensure (FR-011, edge case)

- **Decision**: a host-level advisory file lock (`flock` on `~/.devedge/cluster-<name>.lock`) wraps
  ensure; inside the lock, re-check existence (`k3d cluster list`) and create only if absent.
- **Rationale**: two `de project up` invocations racing to create `devedge` must yield exactly one
  cluster. Lock + recheck is simple, crash-safe (advisory lock releases on process exit), and needs
  no daemon coordination.
- **Alternatives rejected**: daemon-serialized creation (adds an RPC + daemon state for a rare race);
  optimistic create-and-ignore-"already exists" (k3d's error on duplicate is not reliably
  distinguishable and can leave a partially-created cluster).

## D8 — No silent global kube-context mutation (FR-013)

- **Decision**: never run `kubectl config use-context`; every cluster operation passes
  `--context`/`--kube-context` explicitly. The wrapper command may export a scoped `KUBECONFIG` for
  the wrapped process but must not alter the user's default context.
- **Rationale**: the existing `Bootstrap`, `helm.Helm`, and `PortForward` code already pass explicit
  contexts; preserving this keeps the user's environment untouched (Principle V — safety).

## D9 — Config additions (FR-010, FR-016, FR-017)

- **Decision**: `ServiceSpec` gains an optional `cluster:` block: `cluster.dedicated: true` (FR-010,
  dedicated-cluster opt-in, default false). The per-dependency dedicated-instance opt-in (FR-016) is
  expressed as `dependencies[].dedicated: true` (default false, rare). The explicit-sharing knob
  (FR-017) has **no concrete shareable resource type today** (only postgres/redis, isolated by
  default), so the field is **not added now** — recorded as forward-looking; the principle is
  honored by *defaulting to private* and offering the dedicated-cluster outlet.
- **Rationale**: additive, strictly-decoded (matches `ServiceConfig` strictness); avoids inventing a
  sharing mechanism for a resource type that doesn't exist yet (scope gate).
- **Alternatives rejected**: a generic `resources[]` sharing list now (no consumer; speculative).

## Deferred non-functional item (from /speckit.clarify)

- **Timing targets (resolved here)**: reuse-resolution ≤ ~2s; first-time ensure default timeout 300s
  (k3d `create --timeout 180s` + bootstrap), surfaced with progress; teardown best-effort + bounded,
  always attempted. These become measurable checks in the e2e + the verification gate.
