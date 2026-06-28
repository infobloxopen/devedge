# Feature Specification: `kind: Cell` + `de cell` — cell-based deployment and routing

**Feature Branch**: `feature/013-de-cell`
**Created**: 2026-06-28
**Status**: Done

## Background

Cell-based development lets operators deploy version-pinned service instances
("cells") and route subsets of tenants to them. The SDK's `cells` package
(FileTable + MoveController + Campaign) is the routing-plane engine; the CLI
surfaces it through `de cell` and a `kind: Cell` resource.

## Acceptance Criteria

- **AC-001**: `kind: Cell` parses strictly (apiVersion + metadata.name + spec.{service,cell} required;
  replicas defaults to 1); round-trips cleanly through `MarshalCell`; dispatches via `ParseResource`.
- **AC-002**: `de cell create [--service --image|--version --cell --replicas | --from-file] [--dry-run]`
  renders (dry-run) or installs (`helm upgrade --install`) a per-cell "service" chart release named
  `<service>-<cell>`.
- **AC-003**: `de cell down --service --cell [--purge-routes]` uninstalls the Helm release; with
  `--purge-routes` deletes every routing-table entry whose ActiveCell matches, reverting tenants to
  the default cell.
- **AC-004**: `de cell status [--routes-file]` lists tenant routes grouped by cell (tenant, epoch,
  state, budget remaining); lists deployed Helm releases best-effort when a cluster is reachable.
- **AC-005**: `de cell assign --tenant --cell` places a tenant on a cell via `MoveController.Assign`
  (creates epoch-1 on first placement; idempotent).
- **AC-006**: `de cell move --tenant --to [--from] [--drain-window] [--force]` drives
  `MoveController.Move` with a timed drainer; refuses over-budget moves unless `--force`.
- **AC-007**: `de cell rebalance --cells a,b,c [--policy round-robin|least-loaded|sticky]
  [--max-concurrent N]` builds a plan via `PlanFromPolicy` and runs it with the `Campaign` API;
  prints moved/skipped/failed counts and budget remaining.
- **AC-008**: Routes file defaults to `.devedge/cells/routes.json`; parent dirs created on first write.
- **AC-009**: `go build ./... && go test ./...` green; gofmt-clean; no cluster required for unit tests.

## Out of Scope

- Flux/HelmRelease, Gateway-API, NetworkPolicy.
- Docker Compose deploy path.
- etcd/CR routing backend (FileTable is the local backend).
- New apiVersion domain.
