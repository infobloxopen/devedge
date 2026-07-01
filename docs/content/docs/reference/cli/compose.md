---
title: de compose
---

> Generated from `de compose --help`. Run `make docs-cli` to refresh.

## `de compose`

```text
Compose several service modules into ONE host process.

A 'kind: Composition' file lists member modules (each an importable package
exposing a zero-arg Module() constructor). 'de compose build' generates a
cmd/<name>/main.go that imports those modules and runs them via
servicekit.Run — static composition, no Go plugins. The same modules run
standalone or composed by changing the host, not the module.

Usage:
  de compose [command]

Available Commands:
  add         Add a member module to the composition
  build       Generate cmd/<name>/main.go + go.mod + composition.lock
  chart       Render Helm deploy artifacts for the composition (WS-012 P6)
  init        Scaffold a kind: Composition file
  remove      Remove a member module from the composition
  test        Smoke-test the composition (servicekittest.AssertComposition)
  tidy        Validate member modules: descriptor conflicts + version compatibility
  up          Provision shared deps + register the composition's routes

Flags:
  -h, --help   help for compose

Use "de compose [command] --help" for more information about a command.
```

### `de compose add`

```text
Add a member module to the composition

Usage:
  de compose add MODULE[@VERSION] [flags]

Flags:
      --config-prefix string   config namespace prefix (defaults to name)
  -f, --file string            composition config file (default "composition.yaml")
  -h, --help                   help for add
      --name string            member name (defaults to the import path's last segment)
      --schema string          DB schema for this module (defaults to name)
```

### `de compose build`

```text
Generate the STATIC composed-binary sources from the composition:
a cmd/<name>/main.go that imports the member modules and calls servicekit.Run,
a go.mod for the composed binary, and a composition.lock pinning the members +
SDK + toolchain. No Go plugins — the modules are imported, not loaded.

Usage:
  de compose build [flags]

Flags:
  -f, --file string          composition config file (default "composition.yaml")
  -h, --help                 help for build
      --module-path string   Go module path for the generated binary
  -o, --out string           base output directory (defaults to the composition file's dir)
```

### `de compose chart`

```text
Render Helm deploy artifacts for a 'kind: Composition' from a single
descriptor set, supporting three deployment topologies:

  single-binary  — ONE Deployment running the composed binary + one Ingress per
                   member route. (default; also set via spec.runtime.mode)
  multi-daemon   — one Deployment per member module with member-owned routes.
  hybrid         — composed binary for most members; members with
                   failurePolicy: dedicated-required get their own Deployment.

The shared database (spec.database) is provisioned ONCE and expressed as a
DependencyClaim in each workload's chart. Module-namespace isolation is reflected
in the values (compositionSchemas) — the servicekit runtime performs the actual
schema namespacing. Rendering reuses the 'service' embedded chart (same path as
'de project chart').

Usage:
  de compose chart [flags]

Flags:
  -f, --file string   composition config file (default "composition.yaml")
  -h, --help          help for chart
      --mode string   topology override: single-binary | multi-daemon | hybrid (default: from spec.runtime.mode)
  -o, --out string    output base directory (default: chart-<name>)
```

### `de compose init`

```text
Scaffold a kind: Composition file

Usage:
  de compose init NAME [flags]

Flags:
  -f, --file string   composition config file (default "composition.yaml")
  -h, --help          help for init
```

### `de compose remove`

```text
Remove a member module from the composition

Usage:
  de compose remove NAME [flags]

Flags:
  -f, --file string   composition config file (default "composition.yaml")
  -h, --help          help for remove
```

### `de compose test`

```text
Run the composition smoke test: AssertComposition validates the descriptor
union, boots the composed host over the union (the server's fail-closed
completeness gate), and shuts down cleanly. With no shared DB + migrations
configured it runs entirely in-process (no Docker); the real-DB path runs only
when the modules declare migrations and a shared database is configured.

Usage:
  de compose test [flags]

Flags:
  -f, --file string   composition config file (default "composition.yaml")
  -h, --help          help for test
```

### `de compose tidy`

```text
Resolve the composition's member modules and validate the descriptor union
(unique IDs; no duplicate gRPC service / HTTP route prefix / permission names; a
coherent event graph) plus version compatibility, reporting any conflict.

Modules linked into 'de' (and the test fixtures) are validated in-process;
external modules that 'de' does not link are reported as unresolved (they are
still buildable via 'de compose build', which compiles them statically).

Usage:
  de compose tidy [flags]

Flags:
  -f, --file string   composition config file (default "composition.yaml")
  -h, --help          help for tidy
```

### `de compose up`

```text
Provision the composition's shared dependencies (its shared database) and
register the aggregated member routes through the edge — reusing the same
cluster-resolve + dependency-provision + route-register sequencing as
'de project up'. The composed host binary itself is built with
'de compose build' + 'go build'.

Usage:
  de compose up [flags]

Flags:
      --deploy        also deploy the composed workload (opt-in)
  -f, --file string   composition config file (default "composition.yaml")
  -h, --help          help for up
```

