# Changelog

All notable changes to this project will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

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

### Internal

- `internal/cluster/topology.go`: `Environment` type, `DetectEnvironment`,
  `ClusterTarget`, `Resolve`, `RunID`, `ProjectSlug`.
- `internal/cluster/ensure.go`: `Ensurer` with idempotent `EnsureCluster`,
  `EnsureEphemeral`, and `Teardown`; host-level flock; injectable bootstrap
  and probe seams for unit testing.
- Daemon `ApplyDependencies` request gains optional `KubeContext` and
  `Namespace` fields; empty `KubeContext` preserves the existing behavior
  (backward compatible).
