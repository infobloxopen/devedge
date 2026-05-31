---
name: build-run
description: Build devedge binaries and run the daemon to smoke-test a change end to end. Use when you need to compile, install, start devedged, or manually exercise the de CLI against a real route or project file.
---

# Build and run devedge

## Build

- All binaries → `bin/`: `make build` (produces `de`, `devedged`, `devedge-dns-webhook`)
- Install to GOBIN: `make install`

## Run the daemon

- `bin/devedged` starts the control plane (HTTP API over Unix socket + TCP, Traefik subprocess).
- `bin/de install` / `bin/de start` / `bin/de stop` manage it as a platform service
  (macOS LaunchAgent / Linux systemd).
- `bin/de doctor` / `bin/de status` report environment + daemon health.

## Smoke a single route

1. `bin/de register web.foo.dev.test http://127.0.0.1:3000`
2. `bin/de ls` — confirm the route is active.
3. `curl -k https://web.foo.dev.test` — confirm HTTPS termination + forwarding.
4. `bin/de unregister web.foo.dev.test`

## Project mode

- `bin/de project up -f devedge.yaml` registers all routes from a project file
  (add `--watch` to keep leases alive).
- `bin/de project down` removes them.
