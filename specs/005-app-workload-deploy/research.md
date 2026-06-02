# Phase 0 Research: Deploy the app workload onto the resolved cluster

Engineering decisions behind the design. The two highest-impact choices (image source, opt-in vs.
replace) were settled in the spec's 2026-06-02 Clarifications; this file records the supporting
decisions, especially how a *deployed* workload reaches its dependencies.

## D1 — Where deploy logic lives

- **Decision**: New `internal/deploy` package owns the orchestration (resolve image source → ensure the
  image is in the cluster → render + install the `service` chart with in-cluster dependency wiring →
  wait for Ready). Image build/load sits behind an `ImageBuilder` adapter (`docker build` + `k3d image
  import`). The CLI (`cmd/de`) wires the resolved `ClusterTarget` (004) + config into the Deployer.
- **Rationale**: Principle IV (portable core, explicit adapters): orchestration is platform-agnostic and
  unit-testable against a fake builder + fake helm; only the adapter shells out to docker/k3d.
- **Alternatives rejected**: put it all in `cmd/de` (leaks policy into the CLI, untestable without a
  binary); extend `internal/depruntime` (that package is dependency-runtime, not app workloads — different
  concern).

## D2 — Image source: reference by default, build when declared (FR-011)

- **Decision**: `spec.workload.image` (a reference) is deployed as-is. `spec.workload.build` (context +
  optional dockerfile) triggers `docker build -t <derived-tag> <context>` then `k3d image import
  <derived-tag> -c <clusterName>` to load it into the resolved cluster, with no external registry. Exactly
  one of `image`/`build` is set.
- **Rationale**: matches the clarified decision; `k3d image import` is the standard way to run a
  locally-built image in k3d without a registry, keeping the dev loop self-contained (Principle I).
- **Alternatives rejected**: always require a registry (more setup, fails the minimal-setup principle);
  always build (forces a Dockerfile even when the user has a published image).

## D3 — Opt-in deploy (FR-010)

- **Decision**: `de project up --deploy` opts a run into deploying the workload; without `--deploy`,
  behavior is exactly today's (local-run + deps). `--deploy` requires `spec.workload` to be declared.
- **Rationale**: FR-010 — local-run stays the default fast inner loop; deploy is explicit and discoverable.
  A flag (vs. inferring from `spec.workload` presence) keeps the default unchanged for projects that
  declare a workload but still want local-run.
- **Alternatives rejected**: deploy whenever `spec.workload` exists (changes the default UX, surprising);
  a separate `de project deploy` command (splits the lifecycle; `up` is the natural entry point).

## D4 — How a deployed workload reaches its dependencies (the key decision)

- **Decision**: A deployed workload connects to its dependency over the **in-cluster Service DNS** of the
  shared per-engine instance (e.g. `devedge-postgres.devedge-deps.svc.cluster.local:5432`) using 003's
  per-service binding (database/role/password). The connection is delivered as an **in-cluster Secret**
  (`<service>-<dep>-dsn`, key `dsn`) that the `service` chart's Deployment already references via
  `secretKeyRef`. The daemon — which holds the binding credentials and provisions the instance — emits
  this Secret into the resolved cluster as part of deploy-time dependency provisioning, deriving the DSN
  from the binding + the in-cluster Service host (not the host port-forward).
- **Rationale**: local-run reaches deps via a host port-forward + hotload DSN file; a pod cannot use
  `127.0.0.1:<forwardport>`. The pod must use the cluster-internal Service DNS. The daemon already owns
  the binding creds (they are deliberately never returned to the CLI), so it is the right place to
  materialize the in-cluster Secret. This reuses 003's binding unchanged — same database/role/password,
  different reachable host — so the default dependency contract is untouched (plan note 1).
- **Alternatives rejected**: have the CLI read the host DSN file and rewrite host:port (the file holds the
  real password but the in-cluster host differs, and pushing creds back through the CLI breaks 003's
  "creds live only in the DSN file/daemon" boundary); run a per-workload sidecar proxy to the
  port-forward (heavyweight, fragile). 

## D5 — Routing the dev hostname to the workload (FR-004)

- **Decision**: The `service` chart gains an **Ingress** annotated `devedge.io/expose=true` for the
  service's dev hostname. The external-dns webhook installed by 004's `Bootstrap` + the existing ingress
  watch (`de cluster watch` / `internal/k3d`) registers the route with the daemon, which routes the
  hostname to the cluster ingress → the workload. No new addressing scheme.
- **Rationale**: reuses the in-cluster routing path devedge already installs and watches (004 bootstrap),
  so a deployed service is reachable over the same dev hostname model as everything else (Principle I).
- **Alternatives rejected**: the CLI directly registers a daemon route to the cluster ingress (duplicates
  the watch path and doesn't track the workload's lifecycle/readiness).

## D6 — Co-existence + naming (FR-008)

- **Decision**: one Helm release per service, named from the service slug (reuse `cluster.ProjectSlug`),
  installed into the resolved cluster's dependency namespace alongside its deps; resources carry
  per-service selector labels. Two co-located deployed services get distinct releases/labels/ingress hosts.
- **Rationale**: mirrors 004's per-service-unique naming; the chart already labels by release/name, so
  distinct releases coexist cleanly (same property 004 verified for dedicated instances).
- **Alternatives rejected**: a shared release (couples projects); a namespace-per-project (no consumer yet,
  speculative — 003/004 keep shared deps in one namespace).

## D7 — One running instance at a time (FR-010)

- **Decision**: deploy is opt-in per run, so the developer chooses local **or** cluster. When `--deploy`
  is used, the in-cluster Ingress owns the dev hostname; the local-run daemon route for that hostname is
  not also registered in the same run. Running the same service both locally and in-cluster for the same
  hostname simultaneously is explicitly unsupported and documented.
- **Rationale**: FR-010 — exactly one resolvable instance per hostname avoids confusing split-brain
  routing. Opt-in-per-run makes the active mode unambiguous.

## D8 — Teardown (FR-006)

- **Decision**: `de project down` additionally `helm uninstall`s the service's workload release on the
  resolved cluster (footprint-only); the shared cluster and other projects are untouched. Dependency data
  follows existing `--clean` semantics (003); a dedicated-cluster project's `--clean` still removes only
  its own cluster (004).
- **Rationale**: FR-006 — down removes exactly the requesting service's footprint, now including its
  workload.

## Deferred non-functional items

- **Timing**: reference deploy bounded by `helm upgrade --install --wait` + readiness (default 300s);
  build path adds a bounded `docker build` + `k3d image import`; redeploy with no image change is a
  rollout no-op. These become measurable checks in the e2e + verification gate.
