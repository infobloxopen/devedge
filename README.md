# devedge

A local development edge router and name registry.

Devedge gives every project stable HTTPS hostnames on one shared 80/443 entry
point, and lets host apps, containers, and k3d clusters register routes
dynamically.

## Quick start

```bash
make build
./bin/de version
```

## Development

```bash
make test    # run all tests
make lint    # go vet
make build   # build de + devedged
```

## Architecture

- `cmd/de` — CLI for developers and project automation
- `cmd/devedged` — background daemon (control plane)
- `internal/registry` — lease-based route registry
- `internal/reconciler` — event-driven sync loop
- `internal/render` — Traefik dynamic config file generation
- `pkg/types` — shared domain types (Route, etc.)
- `pkg/config` — project configuration parser (devedge.yaml)

See [product_vision.md](product_vision.md) for the full design.
