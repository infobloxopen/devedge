# Changelog

All notable changes to this project will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

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
