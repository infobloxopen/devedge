---
title: de api
---

> Generated from `de api --help`. Run `make docs-cli` to refresh.

## `de api`

```text
API lifecycle operations (publish, ...)

Usage:
  de api [command]

Available Commands:
  publish     Publish a service's OpenAPI v3 spec to the apx catalog

Flags:
  -h, --help   help for api

Use "de api [command] --help" for more information about a command.
```

### `de api publish`

```text
Publish a service's public API as an OpenAPI v3 spec to the apx catalog.

Steps performed:
  1. Run 'make generate' in the service directory to produce a fresh
     openapi/<svc>.openapi.yaml (skip with --skip-generate).
  2. Arrange the spec into the apx directory layout:
       openapi/<domain>/<svc>/<line>/<svc>.openapi.yaml
     where <svc> is the last segment of --api-id.
  3. Run 'apx release prepare openapi/<domain>/<svc>/<line> --version <v> ...'
     and, if --submit is set, also 'apx release submit'.

Without --submit the next-step apx commands are printed for you to run manually
after reviewing the spec and the PR that prepare opens on the canonical repo.

Requires apx on PATH. Install via:
  go install github.com/infobloxopen/apx@latest

Examples:
  # Prepare only (default) — prints the two follow-on commands:
  de api publish \
    --domain platform.data \
    --api-id openapi/platform.data/orders/v1 \
    --version v0.1.0 \
    --lifecycle beta \
    --canonical-repo github.com/infobloxopen/apis

  # Prepare + submit in one shot:
  de api publish \
    --domain platform.data \
    --api-id openapi/platform.data/orders/v1 \
    --version v0.1.0 \
    --lifecycle stable \
    --canonical-repo github.com/infobloxopen/apis \
    --submit

Usage:
  de api publish [flags]

Flags:
      --api-id string           Full apx API ID, e.g. openapi/platform.data/orders/v1 (required)
      --canonical-repo string   Canonical APIs repo, e.g. github.com/infobloxopen/apis (required)
      --domain string           API domain segment, e.g. platform.data (required if not embedded in --api-id)
  -h, --help                    help for publish
      --lifecycle string        API lifecycle: beta or stable (default "beta")
      --service-dir string      Service root directory (default: current working directory)
      --skip-generate           Skip 'make generate'; use the existing openapi/<svc>.openapi.yaml
      --submit                  Also run 'apx release submit' after prepare (opens PR)
      --version string          Semantic version to publish, e.g. v0.1.0 (required)
```

