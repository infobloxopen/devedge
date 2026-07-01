---
title: de new
---

> Generated from `de new --help`. Run `make docs-cli` to refresh.

## `de new`

```text
Scaffold a new artifact (service, ...)

Usage:
  de new [command]

Available Commands:
  service     Scaffold a new apx-native service and route it through the edge

Flags:
  -h, --help   help for new

Use "de new [command] --help" for more information about a command.
```

### `de new service`

```text
Scaffold a new apx-native, authz-gated, persisting service.

This is a thin driver over the devedge-sdk scaffold: it forwards to
'devedge-sdk new service' for the heavy lifting (apx + buf wiring, an
annotated proto, generated models + repository + server), then emits a
devedge.yaml routing the service's HTTP/JSON gateway through the local
edge so 'de project up' serves it over stable HTTPS.

Requires the devedge-sdk binary on PATH:

    go install github.com/infobloxopen/devedge-sdk/cmd/devedge-sdk@latest

Flags after a '--' separator are forwarded verbatim to devedge-sdk
(e.g. --module, --org, --force, --no-generate).

Examples:
  de new service orders --resource Order
  de new service notes --resource Note --backend ent
  de new service orders --resource Order --dir ./services/orders -- --module github.com/acme/orders --force

Usage:
  de new service NAME [-- DEVEDGE_SDK_FLAGS...] [flags]

Flags:
      --backend string    persistence backend: gorm (default) or ent
      --dir string        target directory (defaults to the service name)
  -h, --help              help for service
      --resource string   singular resource type name (e.g. Order); devedge-sdk defaults it from NAME
```

