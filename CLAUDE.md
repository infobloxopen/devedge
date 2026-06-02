# Devedge — Claude Code Instructions

## Constitution (read first)

All work MUST follow the project constitution at `.specify/memory/constitution.md`.
Read and apply its principles and quality gates before planning, speccing, or implementing.
The constitution takes precedence over any default behavior.

## Commit Messages

**NEVER add any AI or LLM attribution to commit messages.** No `Co-Authored-By`, no
"Generated with", no mention of any AI tool or model. Commit messages MUST only describe
the change and its intent.

## Agentic Delivery Lifecycle

Work proceeds **one feature at a time** through a fixed loop built on Spec Kit. Do not skip
phases; each phase has a gate that must pass before the next begins.

| Phase | Command(s) | Model | Gate to advance |
|-------|-----------|-------|-----------------|
| **Propose** | `/speckit.specify` | Opus 4.8 | Spec has acceptance criteria + failure modes |
| **Analyze** | `/speckit.clarify` | Opus 4.8 | Ambiguities resolved |
| **Plan** | `/speckit.plan` → `/speckit.tasks` → `/speckit.analyze` | Opus 4.8 | Tasks complexity-tagged; cross-artifact consistency gate clean |
| **Implement** | `/speckit.implement` | Sonnet `[S]` / Opus `[C]` | Tasks `[X]`; tests green |
| **QA** | `/verify-change` → `/speckit.checklist` | Opus | Functional + scope gates pass |
| **Document** | docs update | Sonnet | README / CLAUDE / CHANGELOG current |

Then move on to the next feature.

### Model routing (spend discipline)

- **Planning is always Opus 4.8.** Thinking hard once is cheaper than replanning.
- Every task in `tasks.md` is tagged `[S]` (simple/mechanical) or `[C]` (complex) during
  `/speckit.tasks`. **Untagged tasks block implementation** (`/route-tasks` enforces this).
- The Opus orchestrator dispatches `[S]` tasks to **Sonnet subagents** (`Agent` tool with
  `model: sonnet`) and keeps `[C]` tasks on Opus.
- **Escalation:** an `[S]` task that fails QA (red tests or rework) is re-tagged `[C]`,
  redone on Opus, and the miss recorded. When escalations cluster in an area, that area
  defaults to Opus. *If Sonnet causes repeated rework, Opus is the model.*

### Verification gate — do not over-build

After every implement, `/verify-change` runs (enforced as the Spec Kit `after_implement` hook).
Both checks must pass:

1. **Functional** — `make build` + `make lint` + unit + integration green; e2e (k3d) REQUIRED
   when the change touches routing, DNS, certs, background processes, or dependency
   orchestration (Constitution III). If Docker/k3d is unavailable, say e2e was skipped and
   why — never claim it passed.
2. **Scope** — diff the change against the spec's acceptance criteria. Anything that does not
   trace to a criterion or a task (speculative abstraction, unused extension points,
   gold-plating) **fails the gate even if tests pass**.

## Skills (use before rediscovering)

Reusable, low-token procedures live in `.claude/skills/`. Invoke them instead of re-deriving
commands:

- `run-tests` — unit + integration + e2e layers; per-package runs.
- `build-run` — build binaries, run `devedged`, smoke a route.
- `verify-change` — the full QA gate above.

When a mechanical step is repeated across features, promote it to a skill to cut tokens.
See `.claude/skills/README.md` for the template and conventions.

<!-- The sections below are maintained by Spec Kit (update-agent-context.sh). -->

## Active Technologies
- Go 1.25.5 (from `go.mod`) (001-fix-dns-udp-bind)
- No new persistent storage. The set of authoritative DNS suffixes (001-fix-dns-udp-bind)
- Go 1.25.5 (from `go.mod`) + `gopkg.in/yaml.v3` (already in use); standard library (002-service-config-kind)
- N/A (parses a local YAML file; no persistence added) (002-service-config-kind)
- Go 1.25.5 + `helm`/`kubectl`/`k3d` CLIs (subprocess; no Helm SDK / client-go), `go:embed` Helm charts, `gopkg.in/yaml.v3` (003-dependency-runtime)
- Shared Postgres/Redis in-cluster (Helm, PVC-backed); real-DSN files under `~/.devedge/`; no DB in devedge itself (003-dependency-runtime)
- Go 1.23 (existing devedge module) + existing `internal/cluster` (k3d `Provider`, `Bootstrap`, `ValidateLocalCluster`, `PortForward`), `internal/helm` (`DefaultNamespace = "devedge-deps"`), `internal/depruntime` (`HelmProvisioner`, `Reconciler`), `internal/daemon` (HTTP API + `DepManager`), `internal/client`, `pkg/config` (`ServiceConfig`). External CLIs already required: `k3d`, `kubectl`, `helm`, container runtime (docker). (004-cluster-topology)
- cluster state is k3d/Docker; per-service dependency data persists in PVCs (003, unchanged); a host-level lockfile under `~/.devedge/` guards concurrent cluster-ensure. (004-cluster-topology)
- Go 1.23 (existing devedge module) + `internal/cluster` (004 — resolved `ClusterTarget` + `EnsureCluster`), (005-app-workload-deploy)
- workload state is k8s (a Helm release per service); per-service dependency data persists in (005-app-workload-deploy)

## Cluster topology model (004-cluster-topology)

`de project up` resolves every project to an explicit cluster target — never the ambient kube context:

- **Developer machine (default)**: shared cluster `devedge`, ensured once, reused by all projects.
  Printed as `cluster: devedge (shared dev)`.
- **CI / ephemeral** (`CI=true` or `--env ci`): dedicated per-run cluster `devedge-ci-<runid>`.
- **`spec.cluster.dedicated: true`**: project-own cluster `devedge-proj-<slug>`.

`de ci run -- <command...>` wraps a command with a full ephemeral-cluster lifecycle (create →
run → always-teardown, deferred + signal-trapped). Uses `DEVEDGE_KUBECONTEXT` to scope the
child process; never mutates the user's global kube context. Implemented in `cmd/de/ci.go`.

Cluster ensure logic (idempotent, host-`flock`, cert-manager bootstrap) lives in
`internal/cluster/ensure.go`. Topology resolution is in `internal/cluster/topology.go`.

## Recent Changes
- 005-app-workload-deploy: Added Go 1.23 (existing devedge module) + `internal/cluster` (004 — resolved `ClusterTarget` + `EnsureCluster`),
- 001-fix-dns-udp-bind: Added Go 1.25.5 (from `go.mod`)
