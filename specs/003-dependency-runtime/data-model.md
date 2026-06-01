# Phase 1 Data Model: Dependency runtime for the Service kind

Entities, fields, relationships, and state. The `Service`/`Dependency` *input* schema is frozen
from feature 002; this feature adds **runtime** state derived from it.

## Input (frozen — from feature 002)

### Dependency (declared)
From `pkg/config` `ServiceConfig.Spec.Dependencies[]`. Unchanged shape:

| Field | Type | Notes |
|-------|------|-------|
| `Name` | string | unique within the service; the dependency's logical name (e.g. `db`, `cache`) |
| `Engine` | string | `postgres` \| `redis` (recognized set; runtime-supported set = same) |
| `Version` | string | optional; selects the instance image tag for the engine |
| `Port` | int | 1–65535; the engine's standard port and the Traefik entrypoint port |

This feature adds only small **helpers** on `Dependency` (no schema change): default port per
engine, derived env-var name (e.g. `DATABASE_URL` / `REDIS_URL` or `<NAME>_DSN`), and the real-DSN
file path.

## Runtime entities (new)

### SharedInstance (per engine, cluster-wide)
The one Postgres / one Redis serving the dev environment.

| Field | Type | Notes |
|-------|------|-------|
| `Engine` | string | `postgres` \| `redis` |
| `Version` | string | resolved image tag (from the first declaration / a default) |
| `Namespace` | string | devedge-managed (e.g. `devedge-deps`) |
| `ReleaseName` | string | Helm release name (e.g. `devedge-postgres`) |
| `ServiceDNS` | string | in-cluster Service the Traefik TCP entrypoint targets |
| `Hostname` | string | stable dev hostname (`postgres.dev.test`) → EdgeIP `127.0.0.2` |
| `EntrypointPort` | int | Traefik TCP entrypoint port (`5432`/`6379`) |

- Rendered + deployed by **Helm** (embedded chart, `helm upgrade --install`).
- StatefulSet + headless Service + **PVC** (durability). One per engine; many services share it.
- Lifecycle is independent of any single service; never dropped by a service `down`.

### ServiceDependencyBinding (per service × dependency)
A service's isolated slice of a `SharedInstance` — the unit `--clean` targets.

| Field | Type | Notes |
|-------|------|-------|
| `Service` | string | `metadata.name` of the declaring Service |
| `DependencyName` | string | the declared `Dependency.Name` |
| `Engine` | string | `postgres` \| `redis` |
| `Isolation` | object | Postgres: `{database, role, password}`; Redis: `{aclUser, password, keyNamespace, dbIndex}` |
| `RealDSN` | string (secret) | e.g. `postgres://role:pw@postgres.dev.test:5432/database` |
| `DSNFilePath` | string | `~/.devedge/services/<service>/<dep>.dsn` (mode `0600`) |
| `EnvVar` | `{name, value}` | value = indirect `fsnotify://<engine>/<DSNFilePath>` (uniform for all engines) |
| `State` | enum | `Pending` → `InstanceReady` → `Provisioned` → `Ready` (or `Failed`) |

- Derived from a declared `Dependency` + its `SharedInstance`.
- `Isolation` is created idempotently via `kubectl exec` (psql / redis-cli) into the instance pod.
- `RealDSN` is written only to `DSNFilePath`; never logged, never placed in `EnvVar.value`.

### DesiredDependencySet (per service, daemon-held)
Desired state the daemon stores and the reconciler converges (mirrors the route registry).

| Field | Type | Notes |
|-------|------|-------|
| `Service` | string | key |
| `Dependencies` | []Dependency | the declared set sent at `project up` |
| `KeepData` | bool | default true; `--clean` on down sets the destructive intent |

## Relationships

```
ServiceConfig (002) ──declares──> Dependency[]
                                     │
                  project up ─sends─ DesiredDependencySet ──> daemon depstore (events)
                                     │
                          reconciler converges each Dependency to a:
                                     │
        Dependency ──realized-as──> ServiceDependencyBinding ──within──> SharedInstance (per engine)
                                     │                                        │
                          writes ─> DSNFilePath (real DSN)            deployed-by Helm (StatefulSet+PVC)
                          reports ─> EnvVar (fsnotify:// indirect)    fronted-by stable hostname + Traefik TCP
```

## State transitions (ServiceDependencyBinding)

```
Pending ──ensure SharedInstance (helm upgrade --install) + readiness──> InstanceReady
InstanceReady ──provision isolation (kubectl exec, idempotent)────────> Provisioned
Provisioned ──write DSN file + verify connect (SELECT 1 / PING)───────> Ready
   any step, on bounded-timeout/error ─────────────────────────────────> Failed (retryable; no half-state)
```

- **Idempotent re-up (FR-008)**: re-running `up` re-enters at the correct state; existing instance +
  isolation are detected and left intact (no data loss).
- **down, default (FR-005/FR-007)**: remove routes/entrypoint registration for the service; **keep**
  `SharedInstance`, PVC, and the binding's `Isolation` (data persists). DSN file may be removed.
- **down --clean (FR-006)**: additionally drop the binding's `Isolation` (Postgres `DROP DATABASE`/
  `DROP ROLE`; Redis `ACL DELUSER` + flush its namespace) — only this service's slice, never the
  shared instance or others.

## Validation rules (carried + added)

- Engine ∈ {`postgres`, `redis`} for runtime (FR-012); a recognized-but-unrunnable engine fails by
  name.
- Derived Postgres identifiers / Redis ACL users are sanitized from the service+dependency names and
  collision-checked (FR-002 isolation).
- Readiness is a real connection probe within a bounded timeout (FR-004), not pod-phase only.
- `helm`/`kubectl` absence is detected up front and reported with an actionable message.
