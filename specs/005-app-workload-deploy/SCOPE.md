# Scope diff — 005-app-workload-deploy (T020)

Every requirement traced to where it is satisfied (code + test). Nothing was built beyond the traced
requirements; local-run behavior is unchanged when `--deploy` is absent (FR-010).

## Functional requirements

| Req | Satisfied by | Verified by |
|-----|--------------|-------------|
| **FR-001** declare a deployable workload | `pkg/config` `WorkloadSpec` (image \| build, port, replicas) + validation + `Workload()`/`WorkloadDeclarer` | `TestParseService_workload*` |
| **FR-002** `up --deploy` deploys onto the resolved cluster | `cmd/de` `--deploy` + `deployWorkload`; `internal/deploy.Deployer` (`helm upgrade --install --wait`) | `TestWorkloadDeploy_e2e` |
| **FR-003** workload receives dep connection + reaches deps | `depruntime` `EnsureConnSecret` + `inClusterDSN` (in-cluster Service DNS + 003 binding); chart deployment `secretKeyRef` | `TestInClusterDSN`; `TestWorkloadDeployDependency_e2e` (env wired + psql connects) |
| **FR-004** reachable over the dev hostname | service chart `ingress.yaml` (`devedge.io/expose`) | `TestWorkloadDeployDependency_e2e` (ingress); `TestServiceChart_render` |
| **FR-005** idempotent deploy/redeploy | `Deployer.Deploy` (`upgrade --install`) | `TestWorkloadDeploy_e2e` (re-deploy, no duplicate) |
| **FR-006** down removes only this workload | `cmd/de` `removeWorkload` → `Deployer.Remove` (`helm uninstall`) | `TestWorkloadDeploy_e2e` (down); `TestWorkloadDeployCoexistence_e2e` (down one) |
| **FR-007** deploy failure actionable, no half-deploy | `Deployer` error wrapping + `helm --install --wait` (atomic-ish: a failed release converges on re-deploy, no half-state) | structural (helm `--wait` semantics); error wrapping in `Deploy` |
| **FR-008** deployed services coexist isolated | per-service release named by `cluster.ProjectSlug`; distinct ingress hosts | `TestWorkloadDeployCoexistence_e2e`; `TestProjectSlug` |
| **FR-009** report placement + status | `deployWorkload` output line + `internal/deploy` structured logging | `TestWorkloadDeploy_e2e` (deploy succeeds + reports) |
| **FR-010** opt-in, complements local-run, one instance at a time | `--deploy` flag; default `up` path unchanged (no deploy without the flag) | default unchanged (no-`--deploy` `up` exercised by existing tests); opt-in by construction |
| **FR-011** image source: reference default, build when declared | `internal/deploy/image.go` `DockerK3dBuilder` (reference passthrough; `docker build` + `k3d image import`) | `TestEnsureImage_reference`, `TestBuildTag`; `TestWorkloadDeployBuild_e2e` |

## Success criteria

| SC | Validated by |
|----|--------------|
| SC-001 one command → running, reachable, deps connected | `TestWorkloadDeploy_e2e` + `TestWorkloadDeployDependency_e2e` |
| SC-002 redeploy, zero duplicate workloads | `TestWorkloadDeploy_e2e` |
| SC-003 two services coexist; down one leaves the other | `TestWorkloadDeployCoexistence_e2e` |
| SC-004 deploy failures actionable, no half-deploy | structural (helm `--wait` + error wrapping); no dedicated failure e2e added |
| SC-005 down removes the workload, never shared/other | `TestWorkloadDeployCoexistence_e2e` (down one) + `Deployer.Remove` footprint scope |

## Design notes (in-scope, recorded)

- **The in-cluster connection Secret is emitted during all dependency provisioning** (not gated on a
  deploy flag). This is the minimal mechanism that lets a deployed workload connect (FR-003) without
  threading a `deploy` flag through the provisioner/daemon/client stack. For local-run the secret is
  created-but-unused (harmless, one small Secret per dependency in `devedge-deps`). Traces to FR-003.
- **The service chart no longer creates the per-dependency Secret** — devedge (the daemon, which holds
  the 003 creds and knows the in-cluster Service host) owns it, so it carries the real DSN before pods
  start. The chart references it via `secretKeyRef`. (`TestServiceChart_render` updated accordingly.)
- **Local-run default is unchanged** (FR-010): a `de project up` without `--deploy` resolves + reports
  the cluster and provisions deps exactly as in 004/003 — no workload is deployed.

## Out-of-scope / not gold-plated

- No new addressing scheme — routing reuses the existing `devedge.io/expose` ingress-watch path (D5).
- No new credential model — the deployed workload reuses 003's per-service binding, only reached over
  the in-cluster Service DNS instead of the host port-forward.
- The orphaned in-cluster connection Secret on `helm uninstall` is left as-is (small, re-created on next
  `up`); cleaning it on down was not built (would be speculative beyond FR-006's footprint intent).
