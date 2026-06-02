# Phase 1 Contracts: CLI deltas + service-chart values

Only what this feature adds or changes; everything else is unchanged.

## CLI contract

### `de project up --deploy` (NEW flag, opt-in)

- **Without `--deploy`** (default): unchanged — resolve + ensure the cluster (when deps exist), provision
  dependencies (003), register routes for local-run. The service process runs locally.
- **With `--deploy`**: after resolve + ensure (004) + dependency provisioning (003) on the resolved
  cluster, devedge **deploys the workload**:
  1. resolve the image — use `spec.workload.image`, or (if `spec.workload.build` is set) `docker build`
     then `k3d image import` into the resolved cluster;
  2. render + `helm upgrade --install --wait` the `service` chart into the resolved cluster with the image,
     port, replicas, dependency env wiring (in-cluster DSN secrets), and an Ingress for `spec.dev.hostname`;
  3. wait for the workload to be Ready; the ingress-watch path registers the dev-hostname route.
- **New observable output**: a line reporting the deploy, e.g.
  `deployed: my-svc -> cluster devedge (1 replica), https://my-svc.dev.test`.
- **Contract guarantees**:
  - D1: `--deploy` with no `spec.workload` exits non-zero with an actionable message.
  - D2: idempotent — re-running `up --deploy` converges the release (rollout), no duplicate workload (FR-005).
  - D3: image-pull/build/deploy failure exits non-zero, actionable, no half-deployed workload (FR-007).
  - D4: the workload connects to its declared dependencies in-cluster and is reachable over
    `spec.dev.hostname` (FR-003/004).
  - D5: never changes the user's current kube context (reuse 004 discipline).

### `de project down` (changed: also removes the workload)

- In addition to releasing routes + dependency bindings (003/004), `down` `helm uninstall`s the service's
  workload release on the resolved cluster. Footprint-only: never the shared cluster or another project
  (FR-006). `--clean` dependency-data semantics (003) and dedicated-cluster removal (004) are unchanged.

### Unchanged

`de project chart` still emits the chart (now including the Ingress); `de cluster ...`, `de ci run`, and
non-`--deploy` `de project up` keep their behavior.

## `service` Helm chart — values contract

The chart is the deploy artifact. Values it consumes:

```yaml
service:
  name: my-svc          # release/resource base name (service slug)
  image: <ref|built-tag>
  port: 8080
  replicas: 1
  hostname: my-svc.dev.test   # NEW: Ingress host (for the devedge.io/expose route)
dependencies:           # per-dependency env wiring (existing shape)
  - name: db
    engine: postgres
    version: ""
    envVar: DATABASE_URL
```

- **Deployment** (existing): runs `service.image` on `service.port`; injects each dependency's `envVar`
  from `secretKeyRef` → Secret `<service.name>-<dep.name>-dsn` key `dsn`.
- **Service** (existing): selects the workload on `service.port`.
- **Ingress** (NEW): host `service.hostname`, annotated `devedge.io/expose=true`, backend → the Service.
- **In-cluster DSN Secret** `<service>-<dep>-dsn` (key `dsn`): produced by the daemon at deploy-time from
  the 003 binding, pointing at the in-cluster Service DNS (see data-model.md). The chart consumes it; it is
  not authored by hand.

## Daemon / client delta (small)

Deploy-time dependency provisioning must additionally materialize the **in-cluster DSN Secret** for each
`(service, dependency)` in the resolved cluster (the daemon holds the binding creds + knows the in-cluster
Service host). The request that provisions dependencies for a deploy signals "also emit the in-cluster
connection secret"; the response shape (per-dependency readiness) is otherwise unchanged. Local-run
(non-deploy) provisioning is unchanged — no in-cluster secret is emitted.
