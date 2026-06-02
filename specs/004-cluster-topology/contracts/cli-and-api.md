# Phase 1 Contracts: CLI commands + daemon API deltas

devedge's external interfaces are its **CLI command surface** and the **daemon HTTP API**. This file
captures only what this feature adds or changes; everything else is unchanged.

## CLI contract

### `de project up` (changed behavior, same flags)

- **Before**: registers routes; if the resource declares dependencies, provisions them via the
  daemon against the **ambient kube context**.
- **After**: first **resolves the environment + target cluster** and **ensures** it exists, then
  provisions/registers against that cluster. New observable output: a line naming the resolved
  cluster, e.g. `cluster: devedge (shared dev)` / `cluster: devedge-proj-myapp (dedicated)` /
  `cluster: devedge-ci-12345 (ephemeral)`.
- **Contract guarantees**:
  - C1: with no cluster present, ensures the shared `devedge` cluster exactly once, then proceeds
    (FR-002); a second `up` reuses it and creates no second cluster (FR-003).
  - C2: reports which cluster the project landed on (FR-003).
  - C3: on cluster-ensure failure (missing `k3d`/runtime, create/bootstrap failure) exits non-zero
    with an actionable, retryable message and leaves no half-created cluster (FR-012).
  - C4: re-running on an already-up project reconciles with no error / no duplicate (FR-011).
  - C5: never changes the user's current kube context (FR-013).
  - C6: a project with no dependencies still resolves the cluster but registers routes exactly as
    before (FR-001 scenario 4).

### `de project down [--clean]` (unchanged surface; isolation guarantee restated)

- Releases only the requesting project's bindings + routes; leaves the shared cluster and other
  projects untouched (FR-005). `--clean` drops this project's dependency data only (003).
- For a project resolved to a **dedicated** cluster, a destructive form removes that dedicated
  cluster without touching the shared cluster (FR-010/US4 AS3).

### `de ci run -- <command...>` (NEW)

The CI/ephemeral wrapper.

- **Behavior**: force `Environment = Ephemeral`; create + bootstrap `devedge-ci-<runid>`; run
  `<command...>` with the resolved context available (scoped `KUBECONFIG`/context, not the global
  default); on exit — success, failure, or signal — **tear the cluster down** via deferred cleanup.
- **Exit code**: propagates the wrapped command's exit code; teardown still runs.
- **Contract guarantees**:
  - C7: a dedicated cluster is created for the run and torn down on every exit path (FR-007).
  - C8: concurrent invocations get distinctly named, isolated clusters (FR-008).
  - C9: the workflow never invokes `k3d` directly (US3 AS1).

### Unchanged

`de cluster create|delete|bootstrap|attach|detach|ls|watch` keep their explicit-name behavior; the
topology resolver reuses their underlying helpers (`CreateAndBootstrap`, `Bootstrap`,
`DeleteAndCleanup`, `Provider`).

## Daemon HTTP API delta

### `POST /v1/dependencies/apply` (or current `ApplyDependencies` route) — additive fields

Request envelope gains an optional target so provisioning lands on the resolved cluster:

```json
{
  "service": "my-svc",
  "kubeContext": "k3d-devedge",
  "namespace": "devedge-deps",
  "dependencies": [
    { "name": "db", "engine": "postgres", "version": "", "port": 5432, "dedicated": false }
  ]
}
```

- `kubeContext` (string, optional): resolved target context. **Empty = current behavior** (current
  context) for backward compatibility.
- `namespace` (string, optional): dependency namespace; default `devedge-deps`.
- `dependencies[].dedicated` (bool, optional): own instance instead of the shared per-engine one
  (FR-016); default false.
- **Response**: unchanged shape (per-dependency readiness + env var + DSN file path, from 003).
- **Server behavior**: selects/creates a `HelmProvisioner` for `kubeContext` (cached per context);
  per-`(service, dependency)` isolation unchanged.

### `DELETE /v1/projects/{project}` and dependency release — unchanged

Release semantics from 003 are unchanged; they operate within the project's footprint on whatever
cluster the bindings were created against.

## Contract tests (Phase 2 will turn these into tasks)

- CT-1 (unit): `DetectEnvironment` precedence — override > `CI` > default.
- CT-2 (unit): `Resolve` produces the right `ClusterTarget` for dev / dedicated / ephemeral, with
  deterministic + collision-free names.
- CT-3 (unit): `ApplyDependencies` with empty `kubeContext` behaves exactly as today (back-compat).
- CT-4 (e2e): C1–C6 (`de project up` ensure/reuse/report/idempotent/no-context-mutation).
- CT-5 (e2e): two projects co-located on `devedge`, per-service isolation holds, down-one leaves the
  other healthy (US2).
- CT-6 (e2e): `de ci run` creates + tears down on both pass and induced failure (US3).
- CT-7 (e2e): the existing dependency e2e suite passes unchanged on shared `devedge` and on a
  dedicated CI cluster (FR-014/SC-004).
