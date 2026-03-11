# devedge

A local development edge router and name registry.

Devedge gives every project stable HTTPS hostnames on one shared 80/443 entry
point, and lets host apps, containers, and k3d clusters register routes
dynamically.

## Quick start

```bash
make build
de install       # install daemon, mkcert CA, DNS config
de start         # start the background daemon
de register web.foo.dev.test http://127.0.0.1:3000
curl https://web.foo.dev.test   # routed through local edge
```

## CLI

```
de install          Install daemon and configure the system
de start            Start the daemon
de stop             Stop the daemon
de doctor           Check system health
de status           Show daemon status
de ui               Open the web dashboard

de register HOST UPSTREAM [--project P] [--ttl 30s]
de unregister HOST
de ls [--json]
de inspect HOST

de project up [-f devedge.yaml]
de project down [PROJECT]

de k3d attach CLUSTER --host api.foo.dev.test [--ingress URL]
de k3d detach CLUSTER
de k3d ls
```

## Project config

```yaml
version: 1
project: foo
defaults:
  ttl: 30s
  tls: true
routes:
  - host: web.foo.dev.test
    upstream: http://127.0.0.1:3000
  - host: api.foo.dev.test
    upstream: http://127.0.0.1:8081
```

## Development

```bash
make test    # run all tests
make lint    # go vet
make build   # build de + devedged
```

## Architecture

```
cmd/de              CLI for developers and project automation
cmd/devedged        background daemon (control plane)
internal/registry   lease-based route registry with conflict detection
internal/reconciler event-driven sync: Traefik configs + /etc/hosts + certs
internal/render     Traefik dynamic + static config generation
internal/daemon     HTTP API over Unix socket + web dashboard
internal/client     Go client for the daemon API
internal/dns        /etc/hosts management + macOS /etc/resolver/ drop-in
internal/certs      mkcert integration for locally-trusted TLS
internal/platform   OS adapters: macOS LaunchAgent, Linux systemd
internal/k3d        k3d cluster discovery and ingress port detection
pkg/types           shared domain types (Route)
pkg/config          devedge.yaml project config parser
```

See [product_vision.md](product_vision.md) for the full design.
