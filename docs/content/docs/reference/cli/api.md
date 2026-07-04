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
  1. Run 'de generate' in the service directory to produce a fresh
     openapi/<svc>.openapi.yaml (skip with --skip-generate).
  2. Arrange the spec into the apx directory layout:
       openapi/<domain>/<svc>/<line>/<svc>.openapi.yaml
     where <svc> is the last segment of --api-id.
  3. Run 'apx release prepare openapi/<domain>/<svc>/<line> --version <v> ...'
     and, if --submit is set, also 'apx release submit'.

Without --submit the next-step apx commands are printed for you to run manually
after reviewing the spec and the PR that prepare opens on the canonical repo.

Client generation (opt-in with --client):
  With --client the command also generates a packaged TypeScript/Angular client
  from the same openapi/<svc>.openapi.yaml via 'apx client generate', writing an
  '@<scope>/<svc>-client' npm module to --client-out (default
  clients/<svc>-client under the service dir). apx orchestrates ng-openapi-gen
  under the hood. Add --publish-client to publish that module to GitHub Packages
  via 'apx client publish --dry-run=false' instead of only generating it.

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

  # Prepare + also generate a typed TS/Angular client package:
  de api publish \
    --domain platform.data \
    --api-id openapi/platform.data/orders/v1 \
    --version v0.1.0 \
    --canonical-repo github.com/infobloxopen/apis \
    --client \
    --client-scope @acme

  # ...and publish the client to GitHub Packages:
  de api publish \
    --domain platform.data \
    --api-id openapi/platform.data/orders/v1 \
    --version v0.1.0 \
    --canonical-repo github.com/infobloxopen/apis \
    --client --client-scope @acme --publish-client

Usage:
  de api publish [flags]

Flags:
      --api-id string             Full apx API ID, e.g. openapi/platform.data/orders/v1 (required)
      --canonical-repo string     Canonical APIs repo, e.g. github.com/infobloxopen/apis (required)
      --client                    After publishing the spec, also generate a typed TS/Angular client via 'apx client generate'
      --client-generator string   client generator: typescript-angular or go (for go, the package name is used as the Go module path) (default "typescript-angular")
      --client-out string         Output directory for the generated client (default: clients/<svc>-client under the service dir)
      --client-scope string       npm scope for the generated client package, e.g. @acme (optional; ignored for --client-generator go)
      --domain string             API domain segment, e.g. platform.data (required if not embedded in --api-id)
  -h, --help                      help for publish
      --lifecycle string          API lifecycle: beta or stable (default "beta")
      --publish-client            Publish the client to GitHub Packages via 'apx client publish --dry-run=false' (implies --client)
      --service-dir string        Service root directory (default: current working directory)
      --skip-generate             Skip 'de generate'; use the existing openapi/<svc>.openapi.yaml
      --submit                    Also run 'apx release submit' after prepare (opens PR)
      --version string            Semantic version to publish, e.g. v0.1.0 (required)
```

