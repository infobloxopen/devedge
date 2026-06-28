# Changelog

All notable changes to this project will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

### Changed

### Fixed

## [0.4.0] - 2026-06-28

### Added

#### Cell-based development — `de cell` (013-de-cell)

- **`de cell` command group** for cell-based development: deploy version-pinned cells for a
  subset of tenants and move tenants between cells safely. This is **isolation, not load
  balancing** — a tenant is pinned to one cell at a time.
  - `create` / `down` — provision or tear down a per-cell service deployment (a parameterized
    Helm "service" chart instance; `down --purge-routes` reverts a cell's tenants to the
    fail-safe default cell).
  - `assign` — sticky first placement of a tenant on a cell.
  - `move` — safe, budget-aware tenant move driven by the devedge-sdk move controller
    (drain-and-cutover with a bounded drain window; the monotonic route-epoch fence preserves
    tenant state across the cut).
  - `rebalance` — even-distribution / blast-radius rebalance across cells via a pluggable
    placement policy (round-robin / least-loaded / sticky), budget-metered.
  - `status` — routes grouped by cell with each tenant's state, epoch, and remaining budget.
- **`kind: Cell`** resource for declaring a cell (service, image/version, cell name,
  controller class) in the existing config family.
- Routes persist in a file-backed routing table (default `.devedge/cells/routes.json`;
  `--routes-file` overrides) so the CLI and a running service share the directory. The
  production etcd / CR-GitOps backend plugs into the same interface.

### Changed

- Bump `devedge-sdk` to **v0.30.0** — the cell-based-development runtime: the synchronous
  routing plane, the 7-phase tenant-move controller, storage/event fencing, and budget metering.

## [0.2.0] - 2026-06-20

### Added

#### Service scaffold (007-service-scaffold-onboarding)

- **`de project init NAME [--dir DIR] [--module MODULE]`**: scaffolds a complete
  authz-governed service project in one command. Generates a `kind: Service` devedge config
  (Postgres dependency + migrations declared), a proto with one example resource where every
  RPC carries an `infoblox.authz.v1.rule` annotation, generated gRPC + REST gateway code, a
  fail-closed server (boot-time gate: an undeclared method refuses to start), an initial
  migration, and a Dockerfile that satisfies the deploy hook's `migrate up` subcommand
  contract. `NAME` must be a lowercase DNS label; init refuses to overwrite a non-empty
  target. Generated projects depend only on released public modules (devedge-sdk, the
  canonical authz annotation module). The onboarding walk-through — scaffold → `make
  generate` → `de project up` → CRUD over `https://NAME.dev.test/v1/webhook-endpoints` →
  `de project up --deploy` — ships as an automated e2e.

- **`de new service NAME`**: thin driver over `devedge-sdk new service` that generates an
  apx-native, devedge-sdk-backed service scaffold and emits a `kind: Config` `devedge.yaml`
  route entry so `de project up` serves it immediately. Complements (does not replace) `de
  project init`; intended for teams already using the SDK's code-generation toolchain.

#### Schema migrations and dev seed (006-storage-migrations-seed)

- **`migrations` and `seed` on a postgres dependency**: optional additive fields on a `postgres`
  dependency in a `kind: Service` `devedge.yaml`. `migrations` points to a directory of versioned
  `NNN_name.up.sql` / `NNN_name.down.sql` files (golang-migrate convention). `seed` points to a
  plain SQL file or directory applied once after migrations for local development. Both fields are
  optional and allowed only on `engine: postgres`; declaring them on any other engine is a
  parse/validate error. Paths resolve under the project root and must exist at parse time; a
  migrations directory must contain at least one `*.up.sql`. `seed` without `migrations` is
  accepted.

- **Apply-before-serve in both modes**: `de project up` brings the dependency's isolated database
  to the declared schema version **before** the dependency is marked ready — in both local-run mode
  (applied by the daemon over the port-forward DSN) and `--deploy` mode (applied by a Helm
  `pre-install`/`pre-upgrade` hook Job before the Deployment rolls). A workload never serves
  against a partial or unmigrated schema.

- **Idempotent and observable**: migration and seed steps are fully idempotent across repeated
  `up` runs. `de project up` reports the outcome for each dependency that declares migrations or
  seed: `migrations: applied N (vX → vY)` / `already current (vN)` / `rolled back (vX → vY)`, and
  `seed: seeded` / `already seeded` / `skipped (CI)`. `de project down --clean` resets the schema,
  seed marker, and the persisted down-migration store; plain `down` preserves them.

- **Up-or-down to target; automatic rollback across image versions**: the migrate step targets a
  version — the highest migration in the current source or image — and migrates up *or* down to
  reach it. Deploying an older image (with a lower target version) therefore rolls the schema back
  automatically with no separate rollback command. This is powered by a **persisted
  down-migration store** that retains applied up/down files so a down step remains available even
  when the current image or branch no longer ships it. In local-run mode the store is a host
  directory under the devedge base dir; in deploy mode it is a per-service PersistentVolumeClaim
  that devedge side-provisions (it persists across deploys; `--clean` removes it). **Note:** the
  fork's auto-recovery does not retro-apply a transactionally-rolled-back migration. For a botched
  migration the reliable fix is `de project down --clean` then `de project up` (rebuilds the
  corrected schema from scratch).

- **Deploy-mode service-image `migrate` subcommand contract (C2)**: in `--deploy` mode devedge
  renders a Helm `pre-install`/`pre-upgrade` hook Job that runs the **service's own image** as
  `<image> migrate up`. Images using `--deploy` with declared migrations MUST provide a `migrate`
  subcommand that reads the DB DSN from `DATABASE_URL` (injected from the per-dependency DSN
  Secret) and the down-store path from `DEVEDGE_DOWNSTORE`, converges the bundled migrations to
  their target version (up or down) using the `github.com/infobloxopen/migrate` engine, and exits
  non-zero on failure. If the image does not provide this subcommand, `de project up --deploy`
  fails with an actionable error; it never silently skips.

- **Dev seed apply-once, CI skip**: when `seed` is declared, devedge applies it once after
  migrations succeed and records it via a `devedge_seed` marker table so re-running `up` neither
  re-applies nor errors. `de project down --clean` removes the marker so the next `up` re-seeds.
  Seed is skipped entirely in CI/ephemeral environments (`de ci run`), which apply schema
  migrations only; tests arrange their own fixtures.

- **Migration engine**: powered by the Infoblox `golang-migrate` fork
  (`github.com/infobloxopen/migrate`, branch `ib`), consumed via a `go.mod` `replace`. Migrations
  use the standard golang-migrate `NNN_name.up.sql`/`.down.sql` convention and a
  `schema_migrations` version table.

#### Daemon toolchain install and doctor (008-daemon-toolchain-install-doctor)

- **`de install` injects tool PATH, HOME, and KUBECONFIG**: the installer now discovers tool
  directories from the invoking user's `PATH` and writes them into the daemon launchd plist's
  `EnvironmentVariables`, so the daemon running under launchd has the same tools available as
  the installing user (resolves helm/kubectl/k3d lookup failures in the daemon process).

- **`de doctor` shows daemon toolchain**: a new "daemon toolchain" section in `de doctor`
  reports per-tool availability as seen from the daemon's own environment (not the shell's).
  Shows `found`/`failed` with the daemon's effective PATH. Reports "skipped (daemon offline)"
  when the daemon is not running, so `doctor` is useful at all stages of setup.

- **Early tool preflight**: `de project up` now checks for required tools (helm, kubectl, k3d)
  before attempting to ensure/create a cluster, so a missing tool is reported immediately
  with an actionable error rather than after wasted cluster-creation time.

#### Health-gated route readiness (010-health-gated-route-readiness)

- **Upstream health probe before route registration**: `de project up` now probes each service's
  upstream health endpoint after the port-forward is established and before registering the route
  with the daemon. Routes are not advertised until the upstream responds healthy, so clients
  never receive a 502 from a route that points at a not-yet-ready service.

#### Client-go native port-forward (009)

- **Replaced kubectl subprocess port-forward with client-go native portforwarder**: the daemon
  no longer shells out to `kubectl port-forward`; port-forwards are managed in-process via the
  Kubernetes client-go port-forward API. This eliminates a class of failures caused by kubectl
  not being in the daemon's PATH and makes port-forward lifecycle management more reliable.

#### Service config `kind: Service` and dependency runtime (002/003)

- **`kind: Service` in `devedge.yaml`**: new project-file variant (alongside `kind: Config`)
  that declares a service's Postgres and Redis dependencies with required name, engine, and
  port; unknown fields are rejected. `de project up`/`down` dispatch on the declared kind.

- **Dependency runtime**: `de project up` provisions declared Postgres and Redis dependencies
  as shared Helm-managed engine instances in the resolved cluster, binds per-service logical
  databases/users, and exposes them to the service via an ephemeral port-forward DSN. DSN
  secrets use the indirect-DSN + real-DSN-file convention. `de project down` releases
  bindings; `--clean` removes the engine instance when no other services share it.

#### Embedded reverse proxy, per-host TLS, DNS, and TCP/SNI (#1–#7)

- **Embedded reverse proxy**: the daemon now runs an in-process reverse proxy instead of
  managing a Traefik subprocess, eliminating a class of PATH and process-management failures.

- **Dynamic per-host TLS signed by mkcert CA** with startup pre-warm: each registered route
  gets a TLS certificate signed by the local mkcert CA; certificates are pre-warmed at daemon
  startup so the first request to a route never triggers a TLS handshake delay.

- **In-process authoritative DNS**: the daemon serves DNS authoritatively for configured
  `.test` suffixes in-process, removing the dependency on external DNS tooling for local
  development.

- **TCP/SNI routing**: non-HTTP TCP services can be routed by SNI hostname, enabling
  gRPC-over-TLS and other TCP protocols alongside HTTP services.

- **`<app>.<cluster>.test` cluster domain naming**: routes for services in a named cluster
  are reachable at `<app>.<cluster>.test` in addition to the per-service hostname.

#### Workload deploy (005-app-workload-deploy)

- **`de project up --deploy`**: new opt-in flag that deploys the service workload into the
  resolved cluster after ensuring the cluster (004) and provisioning dependencies (003).
  Without `--deploy` the command behaves exactly as before (local-run + deps); no default
  behavior is changed. Reports the deploy as
  `deployed: <service> -> cluster <name> (<n> replica(s)) https://<hostname>`.

- **`spec.workload` in `kind: Service`**: new optional block that declares the service's
  deployable workload. Exactly one of `image` (pre-built reference) or `build` (project build)
  must be set; `port` is required; `replicas` defaults to 1.
  - **`spec.workload.image`**: deploy a pre-built container image reference as-is.
  - **`spec.workload.build`**: build the image from the project (`docker build` +
    `k3d image import` into the resolved cluster) — no external registry required.

- **In-cluster dependency connection**: at deploy time devedge creates a Secret named
  `<service>-<dep>-dsn` in the resolved cluster for each declared dependency, pointing at
  the dependency's in-cluster Service DNS with per-service credentials (reusing the 003
  binding). The `service` chart mounts the Secret so the workload connects to its
  dependencies without any manual credential management.

- **Dev-hostname Ingress**: the `service` chart now includes an Ingress annotated
  `devedge.io/expose=true` for `spec.dev.hostname`, so the deployed workload is reachable
  over its stable dev hostname via devedge's existing ingress-watch path.

- **`de project down` removes the workload**: in addition to releasing routes and
  dependency bindings, `down` uninstalls the service's workload Helm release (footprint-only
  — never the shared cluster or another project's workload). No-op for services that were
  never deployed.

#### Cluster topology model (004-cluster-topology)

- **Shared dev cluster auto-ensure**: `de project up` now resolves every
  project to an explicit cluster target instead of relying on the ambient kube
  context. On a developer machine the default target is the single shared cluster
  `devedge`; it is created and bootstrapped (cert-manager, devedge ClusterIssuer,
  external-dns webhook) once and reused by all subsequent projects. The resolved
  cluster is reported as `cluster: <name> (<mode>)` (e.g.
  `cluster: devedge (shared dev)`). The user's global `kubectl` context is never
  changed. Concurrent first-time `de project up` calls are serialized by a
  host-level lock (`~/.devedge/cluster-<name>.lock`).

- **`de ci run -- <command...>`**: new command that wraps any command in a
  full ephemeral-cluster lifecycle. Creates a dedicated `devedge-ci-<runid>`
  cluster (runid from `GITHUB_RUN_ID`, `DEVEDGE_RUN_ID`, or a random token),
  runs the wrapped command with the cluster context available as
  `DEVEDGE_KUBECONTEXT`, and tears the cluster down on every exit path —
  success, failure, or interrupt — via deferred + signal-trapped cleanup.
  The wrapped command's exit code is propagated. Concurrent runs receive
  distinctly named clusters and never interfere.

- **`--env` flag for `de project up`**: explicit environment override —
  `--env dev`, `--env ci`, or `--env ephemeral` — takes precedence over
  auto-detection from the `CI` environment variable. `DEVEDGE_ENV` provides
  the same override without a flag.

- **`spec.cluster.dedicated: true`** in `kind: Service` config: opts a
  project onto its own dedicated cluster (`devedge-proj-<slug>`) instead of
  the shared dev cluster. `de project down --clean` removes the dedicated
  cluster. Projects without the opt-in continue to share `devedge`.

- **`spec.dependencies[].dedicated: true`** in `kind: Service` config: opts
  a single dependency into its own per-service engine instance instead of
  attaching to the shared per-engine Helm release. Use only when per-service
  logical isolation inside the shared instance is not enough; for full
  isolation prefer `cluster.dedicated: true`.

- **Cert-manager bootstrap**: `de cluster bootstrap` (and the auto-ensure
  path) now installs a pinned version of cert-manager on local k3d clusters
  before applying the devedge ClusterIssuer, so the issuer CRD and webhook
  are present. This is a hard-fail on failure; a failed ensure leaves no
  half-created cluster.

### Changed

- `de project up`: now prints `cluster: <name> (<mode>)` before provisioning
  dependencies and registering routes. A project with no dependencies still
  resolves and reports the cluster placement but does not trigger a cluster
  create or ensure.

- `de project down --clean`: for a project that opted into a dedicated cluster,
  also removes that dedicated cluster. Never removes the shared `devedge`
  cluster or another project's resources.

- `kind: Service` strict decode now accepts the optional `spec.cluster` and
  `spec.dependencies[].dedicated` fields; unknown fields are still rejected.

### Fixed

- **mkcert CAROOT resolution for the root daemon**: the daemon now correctly
  locates the mkcert CA root when running as root under launchd, so TLS
  certificates are always signed by the trusted local CA. A `--self-signed`
  fallback flag is available for environments where mkcert is not installed.

- **Daemon PATH inheritance**: the daemon launchd plist now carries the
  installing user's tool PATH, HOME, and KUBECONFIG, fixing helm/kubectl/k3d
  lookup failures that appeared only when the daemon ran under launchd.

- **Relative `-f` path resolution**: `de project up -f <path>` with a relative
  path now resolves correctly on the daemon side, fixing an ENOENT error that
  appeared during the onboarding walk-through.

[Unreleased]: https://github.com/infobloxopen/devedge/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/infobloxopen/devedge/compare/v0.1.0...v0.2.0
