---
title: de cli
---

> Generated from `de cli --help`. Run `make docs-cli` to refresh.

## `de cli`

```text
Scaffold and extend rebrandable devedge CLIs

Usage:
  de cli [command]

Available Commands:
  add         Generate a domain command module and wire it into the CLI shell
  new         Scaffold a new rebrandable CLI shell

Flags:
  -h, --help   help for cli

Use "de cli [command] --help" for more information about a command.
```

### `de cli add`

```text
Generate a domain command module from an enriched OpenAPI v3 spec and
wire it into a CLI shell created by 'de cli new'.

It runs the devedge-cli-sdk cligen generator into <dir>/gen/<domain>, then
regenerates <dir>/domains_gen.go so the shell registers the new domain. The
generated package builds in-module (no nested go.mod). Re-running for the same
domain regenerates it in place.

Examples:
  de cli add --input widgets.openapi.yaml --domain widgets
  de cli add --input ../svc/openapi/svc.openapi.yaml --domain orders --dir ./ib

Usage:
  de cli add [flags]

Flags:
      --app string      rebranded app name (defaults to the shell's appName, else the module basename)
      --dir string      the CLI shell repo directory (defaults to .)
      --domain string   domain command name to add (required)
  -h, --help            help for add
      --input string    path to the enriched OpenAPI v3 spec (required)
```

### `de cli new`

```text
Scaffold a new rebrandable CLI shell wired to the open-core
github.com/infobloxopen/devedge-cli-sdk clikit runtime.

The generated shell is the CLI mirror of a devedge micro-frontend shell: it owns
session construction (binding the generic OIDC device-grant provider from the
active profile, or a --dev static stub) and composes generated "domain command
modules" that consume only the read-only clikit runtime. It builds as-is; add
domains afterwards with 'de cli add'.

Apply an overlay on top of the base scaffold with:
  --preset-dir <path>  a preset directory holding a canonical preset.json
The public CLI ships no proprietary preset; a product-specific preset (concrete
OIDC issuer/client, branding, extra commands) is applied with --preset-dir. A
missing/malformed preset.json fails with a clear error.

Examples:
  de cli new ib
  de cli new ib --module github.com/acme/ib
  de cli new ib --dir ./clis
  de cli new ib --preset-dir ../devedge-cli-sdk-internal/preset/infoblox-cli

Usage:
  de cli new NAME [flags]

Flags:
      --dir string          parent directory to create the CLI in (defaults to .)
  -h, --help                help for new
      --module string       Go module path for the generated CLI (defaults to NAME)
      --preset-dir string   path to a preset directory (with a canonical preset.json) to overlay on top of the base
```

