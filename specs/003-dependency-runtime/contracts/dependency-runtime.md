# Contract: Dependency runtime — CLI, daemon API, DSN/file, and chart

This feature is a CLI + control-plane daemon + library. Contracts: (1) CLI behavior, (2) daemon
HTTP API, (3) the DSN env-var + file convention, (4) the emitted Helm chart shape. The `Service`
YAML schema is unchanged from feature 002 (see `../../002-service-config-kind/contracts/`).

## 1. CLI behavior (`de project up` / `down` / `chart`)

### `de project up [--file devedge.yaml] [--watch]`
For a `Service` declaring dependencies (in addition to existing route registration):

1. Send the declared dependencies to the daemon (desired state).
2. The daemon ensures the shared instance per engine (Helm) and provisions this service's isolated
   database/namespace + credentials, then verifies readiness.
3. On success, for each dependency print:
   - the dependency name + engine,
   - the **env var** the service should set (e.g. `DATABASE_URL=fsnotify://postgres/<abs-path>`),
   - the **real-DSN file path**,
   - "ready".
4. **Failure** is per-dependency, actionable, and retryable (FR-009); `up` exits non-zero if any
   dependency fails. No half-provisioned residue blocks a retry.
5. A `Service`/`Config` with **no** dependencies behaves exactly as in feature 002 (FR-013).

### `de project down [PROJECT] [--clean]`
- **Default (non-destructive)**: release the service's routes + dependency entrypoint registration;
  **keep** the shared instance, PVC, and the service's database/data (FR-005/FR-007). Co-located
  services are unaffected.
- **`--clean`**: additionally drop **only this service's** isolation slice (Postgres `DROP
  DATABASE`/`DROP ROLE`; Redis `ACL DELUSER` + namespace flush). Never drops the shared instance.

### `de project chart [--file devedge.yaml] [-o OUTDIR]`
- Materialize the embedded `service` Helm chart for the Service + its declared dependencies into
  `OUTDIR` (default `./chart`). Dependencies are expressed **abstractly** (a claim template), not as
  the dev shared-instance realization (FR-011).
- Output passes `helm lint`. Requires the `helm` CLI; its absence fails with an actionable message.
- Emits the chart only; does **not** deploy it (FR-010).

### Preconditions / errors
| Condition | Result |
|-----------|--------|
| `helm` or `kubectl` not on PATH | actionable error naming the missing tool; no partial work |
| no dev cluster / not reachable | per-dependency failure: shared instance unavailable (retryable) |
| readiness exceeds timeout | failure naming the dependency + the bounded wait; retryable |
| recognized engine without runtime support | failure naming the engine (FR-012) |
| unknown project on `down` | existing 002 behavior (name required / read from file) |

## 2. Daemon HTTP API (new endpoints; existing route API unchanged)

```
PUT    /v1/services/{service}/dependencies   # body: desired dependency set; upsert + reconcile
GET    /v1/services/{service}/dependencies   # observed state per dependency (State, EnvVar, file)
DELETE /v1/services/{service}/dependencies    # release; ?clean=true to drop isolation slices
```

- `PUT` is idempotent (re-up reconciles to desired; FR-008).
- `GET` returns per-dependency `State` (`Pending|InstanceReady|Provisioned|Ready|Failed`), the env
  var name/value, and the DSN file path — **never** the raw credentials/real DSN.
- `DELETE` default keeps data; `clean=true` drops only this service's isolation.
- Errors are structured and per-dependency (Principle V observability).

## 3. DSN env-var + file convention (uniform across engines)

For each dependency, devedge writes the **real DSN** to a `0600` file and exposes an **indirect
hotload DSN** in an env var. **Same pattern for every engine**, including Redis.

```
file:   ~/.devedge/services/<service>/<dependency>.dsn        # mode 0600
  postgres → postgres://<role>:<pw>@postgres.dev.test:5432/<database>
  redis    → redis://<aclUser>:<pw>@redis.dev.test:6379/<dbIndex>

env var (value is the indirect DSN — strategy scheme / real driver host / file path):
  postgres → DATABASE_URL=fsnotify://postgres/Users/me/.devedge/services/<service>/<dep>.dsn
  redis    → REDIS_URL=fsnotify://redis/Users/me/.devedge/services/<service>/<dep>.dsn
```

- The app sets the env var, imports the hotload driver + `fsnotify` strategy, and opens via
  `sql.Open("hotload", os.Getenv("DATABASE_URL"))` (Postgres). For Redis the **same env-var shape**
  is provided as the convention; the consuming client reads the referenced file (reload is the
  app's concern — no hotload SQL driver applies).
- The real DSN never appears in the env var, logs, or daemon API responses.

## 4. Emitted Helm chart shape (FR-010/FR-011)

`de project chart` produces the standard chart layout (same embedded `service` chart the runtime
uses):

```
chart/
├── Chart.yaml
├── values.yaml            # service image/ports + a `dependencies:` list:
│                          #   - name, engine, version, envVar
└── templates/
    ├── deployment.yaml    # the service Deployment (+ Service)
    ├── dependency-claim.yaml  # per dependency: an ABSTRACT claim, not a concrete instance —
    │                          #   resolves to a shared logical DB in dev and a dedicated instance
    │                          #   via the org DB abstraction in a real cluster (FR-011)
    └── dependency-secret.yaml # the DSN secret the workload mounts (the `fsnotify://…` env + file)
```

- The **dependency-claim + dependency-secret templates are kept separable** from `deployment.yaml`
  (parameterized only by the `dependencies` values + secret name). This preserves the deferred
  "app-owned workload chart" path (see spec Out of Scope / plan decision 7): a future app chart can
  consume just the claim/secret piece while owning its own `Deployment`.
- Dependencies are **abstract claims**, never the dev shared-instance manifests.
- The service env vars in the chart use the **same** `fsnotify://…` indirect-DSN shape, with the
  real DSN mounted from a secret-backed file — identical mental model to local dev.
- Chart renders deterministically (`helm template` is golden-tested) and passes `helm lint`.
