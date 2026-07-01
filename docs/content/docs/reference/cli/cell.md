---
title: de cell
---

> Generated from `de cell --help`. Run `make docs-cli` to refresh.

## `de cell`

```text
Manage cell-based deployments: create and tear down cell instances,
assign tenants to cells, move tenants between cells, and rebalance
the tenant population across a cell fleet.

A cell is a version-pinned deployment of a service. Tenants are routed
to exactly one cell; the routing table (a local JSON file) is the
single source of truth for the current assignment.

Usage:
  de cell [command]

Available Commands:
  assign      Assign a tenant to a cell
  create      Deploy a cell instance (helm upgrade --install)
  down        Uninstall a cell instance
  move        Move a tenant to a different cell
  rebalance   Redistribute tenants across cells using a placement policy
  status      Show tenant routes and per-tenant budget

Flags:
  -h, --help   help for cell

Use "de cell [command] --help" for more information about a command.
```

### `de cell assign`

```text
Assign a tenant to a cell

Usage:
  de cell assign [flags]

Flags:
      --cell string          target cell ID
  -h, --help                 help for assign
      --operator string      operator identifier (for audit)
      --routes-file string   routing table file (default: .devedge/cells/routes.json)
      --tenant string        tenant ID
```

### `de cell create`

```text
Render and install a per-cell "service" Helm chart instance.

A cell instance is a version-pinned deployment of a service identified by
<service>-<cell> (e.g. myapi-canary). Provide flags or a 'kind: Cell' file.

Usage:
  de cell create [flags]

Flags:
      --cell string        cell ID (e.g. canary, v2)
      --dry-run            render chart and print; do not install
  -f, --from-file string   cell config file (kind: Cell)
  -h, --help               help for create
      --image string       full container image reference
  -n, --namespace string   Kubernetes namespace (default: devedge-deps)
      --replicas int       replica count (default 1)
      --service string     service name
      --version string     image version tag (used when --image is not set)
```

### `de cell down`

```text
Uninstall the Helm release for a cell instance (<service>-<cell>).

With --purge-routes, every tenant route whose active cell matches <cell>
is deleted from the routing table, reverting those tenants to the
fail-safe default cell.

Usage:
  de cell down [flags]

Flags:
      --cell string          cell ID
  -h, --help                 help for down
  -n, --namespace string     Kubernetes namespace (default: devedge-deps)
      --purge-routes         delete routing table entries for this cell (tenants revert to default)
      --routes-file string   routing table file (default: .devedge/cells/routes.json)
      --service string       service name
```

### `de cell move`

```text
Move a tenant from its current cell to a target cell.

A timed drain window is observed before committing the cut; the epoch
fence and in-cluster admission gates enforce correctness. The budget
gate refuses the move if the tenant's monthly unavailability allowance
is exhausted — use --force to override.

Usage:
  de cell move [flags]

Flags:
      --drain-window duration   time to wait for in-flight work to drain (default 5s)
      --force                   bypass budget gate
      --from string             source cell ID (auto-detected from routing table when not set)
  -h, --help                    help for move
      --operator string         operator identifier (for audit)
      --routes-file string      routing table file (default: .devedge/cells/routes.json)
      --tenant string           tenant ID
      --to string               target cell ID
```

### `de cell rebalance`

```text
Redistribute tenants across cells.

Reads the current tenant list from the routing table, builds a placement
plan using the chosen policy (round-robin, least-loaded, sticky), and
drives moves via the Campaign API. Budget-aware: over-budget tenants are
skipped (shown in output).

Usage:
  de cell rebalance [flags]

Flags:
      --cells string         comma-separated list of cell IDs (e.g. a,b,c)
  -h, --help                 help for rebalance
      --max-concurrent int   maximum simultaneous moves (<=1 ⇒ sequential) (default 1)
      --operator string      operator identifier (for audit)
      --policy string        placement policy: round-robin | least-loaded | sticky (default "round-robin")
      --routes-file string   routing table file (default: .devedge/cells/routes.json)
```

### `de cell status`

```text
List tenant routes from the routing table, grouped by cell.
Shows each tenant's state, epoch, and remaining unavailability budget.

Deployed Helm releases are listed if a cluster is reachable (best-effort;
skipped cleanly when not reachable).

Usage:
  de cell status [flags]

Flags:
  -h, --help                 help for status
      --routes-file string   routing table file (default: .devedge/cells/routes.json)
      --service string       filter output by service name (informational)
```

