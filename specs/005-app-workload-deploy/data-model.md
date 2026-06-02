# Phase 1 Data Model: Deploy the app workload

The only serialized surface is the config addition; everything else is runtime/behavioral. Fields below
are Go-level shapes the implementation introduces.

## Config addition (`pkg/config` — serialized surface)

Additive to `ServiceSpec`; strictly decoded (unknown fields still rejected). Optional — absent `workload`
means the service is local-run-only (today's behavior), and `de project up --deploy` errors actionably
if there is nothing to deploy.

```yaml
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: my-svc
spec:
  dev:
    hostname: my-svc.dev.test
  workload:                       # NEW, optional
    image: ghcr.io/acme/my-svc:dev   # EITHER a pre-built reference (default path)
    build:                            # OR a build declaration (FR-011)
      context: .
      dockerfile: Dockerfile          # optional, default "Dockerfile"
    port: 8080                        # required when workload is set
    replicas: 1                       # optional, default 1
  dependencies:
    - name: db
      engine: postgres
      port: 5432
```

| Field | Type | Default | Requirement | Validation |
|-------|------|---------|-------------|------------|
| `spec.workload` | object | absent | FR-001 | optional block |
| `spec.workload.image` | string | "" | FR-011 | exactly one of `image`/`build` set when `workload` present |
| `spec.workload.build.context` | string | "" | FR-011 | required if `build` set; a readable path |
| `spec.workload.build.dockerfile` | string | `Dockerfile` | FR-011 | optional |
| `spec.workload.port` | int | — | FR-001 | required when `workload` set; 1–65535 |
| `spec.workload.replicas` | int | `1` | FR-001 | ≥ 1 |

- **Validation rule**: when `spec.workload` is present, exactly one of `image` or `build` MUST be set, and
  `port` MUST be a valid port. `--deploy` without a `spec.workload` is an actionable error.

## Workload (runtime entity)

The runnable form of a service in a cluster.

| Aspect | Value |
|--------|-------|
| Identity | Helm release named from the service slug (`cluster.ProjectSlug(service)`), in the resolved cluster's dependency namespace |
| Image | the declared reference, or the built+imported tag (build path) |
| Reachability | a k8s Service on `workload.port` + an Ingress for `spec.dev.hostname` (annotated `devedge.io/expose=true`) |
| Dependency wiring | env vars (003 names) sourced from in-cluster DSN Secrets (see below) |
| Lifecycle | `absent → built?/loaded? → installed (helm upgrade --install --wait) → Ready`; idempotent re-converge; removed by `helm uninstall` on down |

## In-cluster dependency connection (Secret)

The deploy-time realization of a 003 binding for a pod (distinct from local-run's host DSN file).

| Field | Value |
|-------|-------|
| Secret name | `<service>-<dependency>-dsn` (the `service` chart's Deployment already references this) |
| Key | `dsn` |
| Value | real DSN pointing at the **in-cluster Service DNS** of the shared per-engine instance (e.g. `postgres://<user>:<pw>@devedge-postgres.devedge-deps.svc.cluster.local:5432/<db>`) using 003's per-service `(database, user, password)` binding |
| Producer | the daemon (holds the binding creds), during deploy-time dependency provisioning |

- **Invariant**: same binding identity as 003 (database/role/password unchanged); only the reachable host
  differs (in-cluster Service DNS instead of host port-forward). 003's default (local-run) contract is
  untouched.

## Deployed service footprint

A service's in-cluster presence, additive to its 003 dependency footprint:

- the Helm release (Deployment + Service + Ingress + in-cluster DSN Secrets);
- the route the ingress-watch path registers for `spec.dev.hostname`.

**Cleanup**: `de project down` `helm uninstall`s the release (footprint-only — never the shared cluster or
another project); dependency data follows `--clean` (003); a dedicated-cluster project's `--clean` removes
only its own cluster (004).
