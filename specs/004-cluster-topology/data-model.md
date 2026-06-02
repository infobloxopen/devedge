# Phase 1 Data Model: Cluster topology

Entities are mostly behavioral/runtime (this is a control plane, not a CRUD store). "Fields" below
are Go-level shapes the implementation introduces; the only persisted/serialized surface is the
config additions and the daemon request envelope.

## Environment (enum)

The resolved operating mode.

| Value | Meaning |
|-------|---------|
| `Dev` | developer machine — use the shared dev cluster (default) |
| `Ephemeral` | CI / per-run — use a dedicated ephemeral cluster |

- **Derivation** (D2/FR-009): explicit `DEVEDGE_ENV` / `--ephemeral` override → else truthy `CI` →
  `Ephemeral` → else `Dev`.
- **Invariant**: deterministic for a given process environment; never inferred from cluster state.

## ClusterTarget

The resolved destination for a project's routes + dependencies. Produced by the resolver, consumed
by the CLI and threaded to the daemon.

| Field | Type | Notes |
|-------|------|-------|
| `Name` | string | k3d cluster name (`devedge`, or `devedge-ci-<runid>`, or `devedge-proj-<slug>` for a dedicated opt-in) |
| `KubeContext` | string | `k3d-<Name>` (from `Provider.KubeContext`) |
| `Namespace` | string | dependency namespace on that cluster — `devedge-deps` (shared per-engine, 003) by default |
| `Ephemeral` | bool | true → created per-run and torn down by the wrapper |
| `Dedicated` | bool | true → resolved from the project's `cluster.dedicated` opt-in |

- **Resolution rules** (D3/D4/D5):
  - `Environment == Ephemeral` → `Name = devedge-ci-<runid>`, `Ephemeral = true`.
  - else `cluster.dedicated == true` → `Name = devedge-proj-<slug(project)>`, `Dedicated = true`.
  - else → `Name = devedge` (the shared dev cluster).
- **Invariant**: `Name` is deterministic per (environment, project, config); two distinct projects
  never resolve to the same dedicated name; ephemeral names are per-run unique.

## Shared dev cluster (singleton, per host)

- Identity: name `devedge`, context `k3d-devedge`, ingress host port `8081`.
- **Lifecycle**: ensured lazily on first `de project up` (create + bootstrap, idempotent under a host
  lock); reused thereafter; persistent across projects and restarts; removed only by an explicit
  destructive command (no auto-GC).
- **State transitions**: `absent → ensuring(locked) → present(bootstrapped)`; `present` is terminal
  until explicit deletion. A `present-but-not-bootstrapped` cluster is reconciled (re-bootstrapped),
  not duplicated (edge case).

## Ephemeral cluster

- Identity: name `devedge-ci-<runid>`, created by `de ci run`.
- **Lifecycle**: `create+bootstrap → run wrapped command → teardown (deferred; fires on success,
  failure, or signal)`. A crash leaves a uniquely named, discoverable cluster (no collision).

## Project footprint

A project's isolated presence on a cluster.

- **Today's concrete footprint** = its per-`(service, dependency)` bindings (own database/namespace +
  credentials) inside the shared per-engine instance in `devedge-deps` (003), plus its daemon-level
  routes. Optional: a dedicated dependency instance (FR-016 opt-in).
- **Isolation unit**: the service / its generated Helm chart (per Clarifications terminology).
- **Cleanup**: `de project down` (default) releases the project's bindings + routes only; never the
  shared cluster or another project's footprint. `--clean` drops this project's dependency data (003).

## Config additions (`pkg/config` — serialized surface)

Additive to `ServiceSpec`; strictly decoded (unknown fields still rejected).

```yaml
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: my-svc
spec:
  dev:
    hostname: my-svc.dev.test
  cluster:                 # NEW, optional
    dedicated: true        # FR-010: own cluster instead of the shared one (default false)
  dependencies:
    - name: db
      engine: postgres
      port: 5432
      dedicated: true      # NEW, FR-016: own instance instead of the shared per-engine one (default false, rare)
```

| Field | Type | Default | Requirement | Validation |
|-------|------|---------|-------------|------------|
| `spec.cluster.dedicated` | bool | `false` | FR-010 | none beyond bool |
| `spec.dependencies[].dedicated` | bool | `false` | FR-016 | only meaningful for recognized engines |

- **Not added now**: a generic shared-resource declaration (FR-017). No shareable resource type
  exists (only postgres/redis, private by default); the principle is honored by *defaulting to
  private* + the dedicated-cluster outlet. Recorded as forward-looking in research.md (D9).

## Daemon request envelope (`internal/client` ↔ `internal/daemon`)

`ApplyDependencies` gains a per-request target so provisioning lands on the resolved cluster (FR-013).

| Field | Type | Notes |
|-------|------|-------|
| `KubeContext` | string | resolved target context (empty preserves today's current-context behavior for back-compat) |
| `Namespace` | string | dependency namespace (default `devedge-deps`) |

- The daemon selects/creates a `HelmProvisioner` for `KubeContext` (cached in
  `map[string]*HelmProvisioner`); per-`(service, dependency)` isolation within it is unchanged (003).
