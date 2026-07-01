---
title: de project
---

> Generated from `de project --help`. Run `make docs-cli` to refresh.

## `de project`

```text
Manage project routes

Usage:
  de project [command]

Available Commands:
  chart       Generate a Helm chart for the service and its declared dependencies
  down        Remove all routes for a project
  init        Scaffold a new service project
  up          Register all routes from devedge.yaml

Flags:
  -h, --help   help for project

Use "de project [command] --help" for more information about a command.
```

### `de project chart`

```text
Generate a Helm chart for the service declared in devedge.yaml and its
dependencies. Dependencies are expressed as abstract claims (a shared logical
database in dev; a dedicated instance via the platform DB abstraction in a real
cluster). The chart is emitted only — it is not deployed.

Usage:
  de project chart [flags]

Flags:
  -f, --file string   project config file (default "devedge.yaml")
  -h, --help          help for chart
  -o, --out string    output directory for the chart (default "chart")
```

### `de project down`

```text
Remove all routes for a project

Usage:
  de project down [PROJECT] [flags]

Flags:
      --clean            also destroy this project's dependency data; for a dedicated-cluster project, remove its cluster
  -f, --file string      project config file (to detect a dedicated cluster) (default "devedge.yaml")
  -h, --help             help for down
  -p, --project string   project name
```

### `de project init`

```text
Scaffold a new service project ready for 'de project up'.

The generated project contains everything needed to develop and run a
devedge-managed service:

  - devedge.yaml       devedge Service config (routes, dependencies)
  - proto/             annotated .proto with fail-closed authz annotations
  - authz/             generated fail-closed authz enforcement server
  - migrations/        database migration stubs (SQL + seed)
  - Dockerfile         multi-stage build for the service

After scaffolding, the project is immediately usable:

  cd NAME
  make generate        # run protoc + authz codegen
  de project up        # register routes and start dependencies

For a full walk-through of the generated layout see AGENTS.md inside the
generated project.

Flags:
  --dir     parent directory to create the project in (default: current dir)
  --module  Go module path for the generated go.mod (default: service name)

Usage:
  de project init NAME [flags]

Flags:
      --dir string      parent directory to create the project in (default ".")
  -h, --help            help for init
      --module string   Go module path (default: service name)
```

### `de project up`

```text
Register all routes from devedge.yaml.

With --watch, the command stays running and sends heartbeats to renew
leases. This keeps routes alive for as long as the project is active.
Press Ctrl-C to stop and let leases expire naturally.

Usage:
  de project up [flags]

Flags:
      --deploy        also deploy the service workload into the resolved cluster (opt-in; default is local-run)
      --env string    environment override: dev|ci|ephemeral (default: auto-detect from CI)
  -f, --file string   project config file (default "devedge.yaml")
  -h, --help          help for up
  -w, --watch         stay running and send lease heartbeats
```

