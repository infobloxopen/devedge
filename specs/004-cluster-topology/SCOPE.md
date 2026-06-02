# Scope diff — 004-cluster-topology (T029)

Final trace of every spec requirement to where it is satisfied (code + test). Nothing was built
that does not trace to a requirement; one requirement (FR-017) is **intentionally not built** and
recorded below.

## Functional requirements

| Req | Satisfied by | Verified by |
|-----|--------------|-------------|
| **FR-001** resolve every project to a target from explicit topology | `cluster.Resolve` (`topology.go`); `de project up` wiring (`cmd/de/main.go`) | `TestResolve`; `TestClusterTopology_ensureReuse_e2e` |
| **FR-002** single shared dev cluster, ensured + bootstrapped | `EnsureCluster` (`ensure.go`); `Bootstrap` installs cert-manager (`bootstrap.go`) | `TestEnsureCluster_*`; `TestClusterTopology_ensureReuse_e2e` (cert-manager + ClusterIssuer present) |
| **FR-003** reuse if present, no second cluster | `EnsureCluster` present-path fast reuse | `TestEnsureCluster_reuseIfPresent`; e2e create-once/reuse |
| **FR-004** per-project footprint uniquely named + isolated | 003 per-(service,dependency) isolation (unchanged); `ProjectSlug` for new cluster resources | `TestProjectSlug`; `TestClusterTopology_coexistence_e2e` |
| **FR-005** default down removes only the requesting project's footprint | `projectDownCmd` (routes + deps release only); daemon `Release` per stored target | `TestDepManager_releaseUsesAppliedTarget`; coexistence e2e (down one, other healthy) |
| **FR-006** coexistence via 003 stateful-store isolation | preserved, not rebuilt (003 bindings) | `TestClusterTopology_coexistence_e2e` |
| **FR-007** CI: dedicated ephemeral cluster, torn down on every exit | `EnsureEphemeral`/`Teardown`; `de ci run` deferred+signal-trapped teardown (`ci.go`) | `TestRunCI_teardownAndExitCode`; `TestCIEphemeral_createTeardown_e2e` |
| **FR-008** concurrent CI runs get distinct, isolated clusters | per-run-unique `devedge-ci-<runid>` + free host port | `TestResolve` (unique names); `TestCIEphemeral_createTeardown_e2e` |
| **FR-009** env auto-detect, override wins | `DetectEnvironment` (flag/`DEVEDGE_ENV` > `CI` > dev); `--env` flag | `TestDetectEnvironment` |
| **FR-010** project may declare a dedicated cluster | `spec.cluster.dedicated` → `Resolve` → `devedge-proj-<slug>`; destructive `down --clean` removes it | `TestParseService_dedicatedOptIns`, `TestResolve`; `TestDedicatedCluster_e2e` |
| **FR-011** resolve/ensure/target idempotent | host-lock create-once + reconcile; `helm upgrade --install` | `TestEnsureCluster_createOnceUnderLock`, `_reconcileUnbootstrapped`; e2e idempotent re-up |
| **FR-012** missing prerequisite / failure → actionable, no half-state | `EnsureCluster` error wrapping + cleanup-on-failed-create | `TestEnsureCluster_failedCreateNoLeftover`, `_missingTool` |
| **FR-013** target the resolved cluster, never mutate global context | per-request `Target` threaded to daemon provisioner; `--kubeconfig-switch-context=false` | `TestDepManager_*`; e2e current-context-unchanged assertions |
| **FR-014** same e2e suite on dedicated CI cluster and shared | dep provisioning is topology-agnostic; same code path both ways | `TestCIEphemeral_dependencyParity_e2e` + existing 003 e2e (bare/shared) |
| **FR-015** dedicated opt-in toggle migrates the project | daemon `DepManager.Apply` releases prior target before provisioning the new | `TestDepManager_toggleMigration` |
| **FR-016** deps default to 003 shared per-engine; opt into a dedicated instance | `dependencies[].dedicated` → per-service Helm release (`realprov.go` `instanceFor`) | `TestParseService_dedicatedOptIns`; `TestDedicatedInstance_e2e` |
| **FR-017** resource shared across services only when explicitly declared | **NOT BUILT — intentional (see below)** | n/a |

## Success criteria

| SC | Validated by |
|----|--------------|
| SC-001 clean machine `de project up` comes up on ensured shared cluster | `TestClusterTopology_ensureReuse_e2e` |
| SC-002 two same-named projects coexist isolated on shared cluster | `TestClusterTopology_coexistence_e2e` |
| SC-003 CI dedicated cluster created + torn down 100% (pass/fail) | `TestRunCI_teardownAndExitCode` (induced failure); `TestCIEphemeral_createTeardown_e2e` |
| SC-004 same suite passes on dedicated CI and shared | `TestCIEphemeral_dependencyParity_e2e` + 003 e2e |
| SC-005 dedicated-opt-in project on its own cluster, co-located default on shared | `TestDedicatedCluster_e2e` (resolution + ensure); `TestResolve` |
| SC-006 `de project up` twice → same cluster, no change | `TestEnsureCluster_*`; e2e idempotent reuse |
| SC-007 100% of ensure failures are actionable | `TestEnsureCluster_failedCreateNoLeftover`, `_missingTool` |
| SC-008 two co-located services → isolated Postgres/Redis | `TestClusterTopology_coexistence_e2e` |

## FR-017 — intentionally not built (recorded decision)

FR-017 (a project may declare a resource **shared across services**) has **no concrete shareable
resource type today**: the only stateful resources are Postgres/Redis, which feature 003 isolates
per-service by default. The principle ("private unless explicitly shared") is honored by
**defaulting to private**; the outlet for the opposite need (full isolation) is the dedicated-cluster
opt-in (FR-010). Inventing a generic `resources[]` sharing declaration now would be a speculative
abstraction with no consumer and would **fail the scope gate**. Recorded in `research.md` D9; the
config field is deliberately omitted. Revisit when a genuinely shareable resource type exists.

## Out-of-scope / not gold-plated

- The `internal/cluster` ↔ `internal/k3d` package duplication is **not** touched (plan, Complexity
  Tracking).
- cert-manager installation in `Bootstrap` traces to **FR-002** (a fully bootstrapped cluster is the
  ensure contract); it only runs on local clusters (guarded by `ValidateLocalCluster`), so it is the
  means to FR-002, not new scope.
