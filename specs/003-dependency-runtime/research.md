# Phase 0 Research: Dependency runtime for the Service kind

Decisions that resolve the plan's unknowns. Each: **Decision / Rationale / Alternatives rejected.**

## 1. How the locally-running service reaches an in-cluster dependency

**Decision**: Front each shared engine instance with a **stable dev hostname** (`postgres.dev.test`,
`redis.dev.test`) resolving to the EdgeIP `127.0.0.2` (existing DNS + `/etc/hosts` path), and a
**dedicated Traefik TCP entrypoint** on the engine's standard port (`5432`/`6379`) with a catch-all
`HostSNI("*")` TCP router forwarding to the in-cluster Service. The declared `port` is honored as
the entrypoint port.

**Rationale**: Reuses devedge's identity and existing seams — stable hostnames, EdgeIP, Traefik
(including the TCP/SNI routing added in commit `7f2837c`), and the renderer in `internal/render`.
Keeps "names over raw ports" (Principle I). One shared instance per engine means one entrypoint per
engine — no port collisions, so the standard port can be used verbatim.

**Alternatives rejected**:
- *`kubectl port-forward` managed by the daemon* — an extra long-lived subprocess per engine to
  supervise/restart; fragile and off-identity. Rejected.
- *k3d host port mapping* — k3d port maps are fixed at cluster-create time; can't add a dependency
  port to an existing dev cluster without recreating it. Rejected.
- *SNI multiplexing on one entrypoint* — Postgres/Redis wire protocols are not TLS-SNI by default,
  so a single SNI-routed entrypoint can't distinguish engines; a dedicated non-SNI entrypoint per
  engine is simpler and correct.

## 2. Exact hotload DSN form (FR-003a)

**Decision**: The env var value is `fsnotify://<driver>/<abs-path-to-dsn-file>` — hotload's real
scheme, where the **URL scheme is the config-source strategy** (`fsnotify`), the **host is the real
`database/sql` driver** (`postgres`), and the **path is the file** holding the real DSN. Example:
`fsnotify://postgres/Users/me/.devedge/services/webhooks/db.dsn`. The service imports
`_ "github.com/infobloxopen/hotload"` and `_ "github.com/infobloxopen/hotload/fsnotify"` and calls
`sql.Open("hotload", os.Getenv("DATABASE_URL"))`.

**Rationale**: Verified against the hotload README — the documented form is
`sql.Open("hotload", "fsnotify://postgres/tmp/myconfig.txt")`, with `?forceKill=true` as an optional
query param. devedge must emit exactly this shape or the driver won't resolve the source.

**Alternatives rejected**:
- *Inlining the real DSN in the env var* — defeats hot-reload and exposes credentials in the
  environment; the stakeholder explicitly wants the real DSN in a file and the env var indirect.
- *A `hotload://` scheme* — not what the library uses; the scheme is the strategy name.

## 3. Uniform DSN strategy across engines (Postgres *and* Redis)

**Decision**: **Every** engine uses the same emission pattern — the env var is the indirect
`fsnotify://<driver>/<abs-path-to-dsn-file>` DSN, and the **real DSN** is written to a file. This
holds for Redis (`fsnotify://redis/<abs-path>`, real DSN `redis://<user>:<pw>@redis.dev.test:6379/<db-index>`)
exactly as for Postgres (`fsnotify://postgres/<abs-path>`, real DSN
`postgres://<user>:<pw>@postgres.dev.test:5432/<db>`). The **pattern is the contract**, regardless
of whether a hotload-compatible driver ships for that engine: hotload's stock `database/sql`
strategy backs Postgres today; for Redis, a client that reads the file (and, if it chooses, watches
it) gets the same indirection and reload story. Providing a Redis reloader is the consuming app's
concern, not devedge's.

**Rationale**: Direct instruction — "use the same DSN strategy for redis even if the hotload driver
doesn't support it; it's a pattern." Uniformity means one mental model, one env-var convention, and
one code path in `internal/dsn` for all engines (and it carries identically into the emitted chart's
secret-mounted file). It also matches the spec, which never carved Redis out of FR-003/003a/003b.

**Alternatives rejected**:
- *A different env-var shape for Redis* (e.g. `REDIS_DSN_FILE`) — breaks the single pattern the
  stakeholder wants; forces consumers to special-case engines.
- *Building a Redis hotload driver inside devedge* — out of scope; the pattern stands on its own and
  the app owns its client.

## 4. Shared-instance workload shape, images, and rendering (Helm)

**Decision**: All Kubernetes objects are rendered by **Helm**, not hand-assembled YAML strings.
Ship **minimal in-repo Helm charts** under `internal/helm/charts/{postgres,redis}` (embedded via
`go:embed`), each rendering a **StatefulSet + headless Service + PVC** into a devedge-managed
namespace (e.g. `devedge-deps`) using the official `postgres`/`redis` images. Deploy with the
`helm` **CLI** (`helm upgrade --install --kubeconfig <k3d-context> …`), which is idempotent by
construction (FR-008). StatefulSet+PVC gives stable identity + data durability across `down`/`up`
(FR-005).

**Rationale**: Direct instruction — "if we are using k8s objects, we should use a Helm chart to
render, not string-rendered strings." Helm also unifies the renderer: the dev-runtime instances and
the FR-010 emitted artifact (decision 7) flow through the same chart machinery. Using the `helm`
CLI as a subprocess matches the existing `kubectl`/`k3d` pattern, so no new Go module is added
(no Helm SDK, no client-go). `go:embed` keeps devedge self-contained (no runtime chart-repo
dependency). `helm template` output is golden-tested for determinism (inspectable in CI per the
constitution).

**Alternatives rejected**:
- *Hand-rendered manifest strings + `kubectl apply`* (the original draft) — **rejected on the
  stakeholder's instruction**; two renderers (strings for runtime, chart for the deliverable) is
  also redundant and harder to keep consistent.
- *Helm Go SDK* — heavyweight Go dependency; the CLI subprocess is consistent with current practice
  and sufficient.
- *Bitnami / external upstream charts at runtime* — adds a runtime dependency on an external chart
  repo and less deterministic; in-repo embedded charts are self-contained and pinned.
- *A community Postgres operator* — large CRD surface for one shared dev instance; the org DB
  abstraction is the prod path and is out of scope.

## 5. Per-service database + credential provisioning

**Decision**: Provision via **`kubectl exec` into the shared instance pod**, running `psql`
(`CREATE ROLE … LOGIN PASSWORD …` + `CREATE DATABASE … OWNER …`, guarded to be idempotent) for
Postgres, and `redis-cli ACL SETUSER` + an assigned key namespace / logical DB index for Redis.
`psql`/`redis-cli` ship inside those images, so no new **host** dependency is added. The
`Provisioner` adapter interface (`EnsureInstance`, `EnsureDatabase`, `DropDatabase`, `Ready`) wraps
this; a **fake** implements it for unit/integration tests.

**Rationale**: Reuses the kubectl-subprocess approach already in the repo; keeps provisioning logic
testable behind an adapter (Principle IV). Idempotent SQL keeps re-up safe (FR-008).

**Alternatives rejected**:
- *A Kubernetes Job per provision* — more moving parts and slower feedback than an exec into a
  ready pod; harder to make synchronous for the readiness gate.
- *Connecting from the host with a local `psql`* — adds a host tool dependency devedge can't assume.

## 6. Credential generation, storage, and isolation unit

**Decision**: Generate a **per-(service, dependency)** random password; the Postgres role/database
name and Redis ACL user/namespace are derived deterministically from the service name (sanitized,
collision-avoided). The **isolation unit** that `--clean` drops is the service's database (Postgres)
or its ACL user + namespace keys (Redis) — never the shared instance or PVC. Secrets live only in
the real-DSN file (mode `0600`) under `~/.devedge/services/<service>/`, never in logs or the env var.

**Rationale**: Satisfies FR-002 isolation and FR-006 targeted wipe; keeps secrets out of process
environment and logs (Security & Trust).

**Alternatives rejected**:
- *Shared credentials across services* — violates FR-002 isolation.
- *Storing credentials in the daemon registry on disk unencrypted* — broader exposure than a single
  `0600` DSN file the service already needs to read.

## 7. Helm chart emission — the same chart machinery (FR-010/FR-011)

**Decision**: `de project chart` **materializes the embedded `service` chart**
(`internal/helm/charts/service`: `Chart.yaml`, `values.yaml`, `templates/` for the service's
Deployment/Service, and per dependency a **templated abstract claim**) to an output dir, filling a
`dependencies` values list (`engine`/`version`/env-var name) from the `Service` declaration. This is
the **same chart the dev runtime renders** — not a separate string-rendered artifact. Dependencies
are expressed **abstractly** (a claim template) so the chart resolves to a shared logical DB in dev
and a dedicated instance via the org DB abstraction in a real cluster (FR-011). Validated with
`helm lint`; `helm template` output is golden-tested for determinism. The `helm` CLI is required;
its absence fails with an actionable message. This feature **emits** the chart; it does not deploy
it to prod.

**Rationale**: Per the rendering decision (decision 4), Helm is the one renderer for all k8s
objects, so the emitted artifact and the runtime share templates — no second renderer to keep in
sync. The abstraction keeps the declaration environment-agnostic (FR-011) without binding dev to a
specific prod realization.

**Alternatives rejected**:
- *A separate string-rendered chart generator* — duplicates the runtime renderer and drifts;
  rejected in favor of one embedded chart set.
- *Hard-coding the dev shared-instance realization into the chart* — would break FR-011's
  environment-agnostic requirement and the prod path.

## 8. Readiness detection and timeouts (FR-004)

**Decision**: After apply + provision, poll readiness via the `Provisioner.Ready` check (a trivial
`SELECT 1` / `PING` over `kubectl exec`) with a **bounded timeout (default 60s, configurable)** and
exponential backoff; on timeout, fail loudly and retryably (no half-provisioned residue — FR-009).
Background loops use context cancellation per the constitution.

**Rationale**: Bounded, observable, safe-over-silent (Principle V). A real connectivity probe (not
just "pod Running") is what US1 actually needs.

**Alternatives rejected**:
- *Waiting on pod phase only* — a Running pod isn't necessarily accepting connections; would yield
  false-ready and flaky US1.
- *Unbounded wait* — violates bounded-retry/timeouts requirement.
