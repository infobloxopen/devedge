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
de renew HOST
de ls [--json]
de inspect HOST

de project up [-f devedge.yaml] [--watch]
de project down [PROJECT]

de cluster create CLUSTER [--port 8081]
de cluster delete CLUSTER
de cluster bootstrap CLUSTER [--force]
de cluster attach CLUSTER --host api.foo.dev.test [--ingress URL]
de cluster detach CLUSTER
de cluster ls
de cluster watch CLUSTER

de k3d ...          (alias for de cluster)
```

## Project config

The project configuration follows the Kubernetes resource API structure:

```yaml
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Config
metadata:
  name: foo
  labels:
    team: platform
spec:
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
make build   # build de + devedged + devedge-dns-webhook
```

## Architecture

```
cmd/de                  CLI for developers and project automation
cmd/devedged            background daemon (control plane)
cmd/devedge-dns-webhook external-dns webhook provider for k8s integration
internal/registry       lease-based route registry with conflict detection
internal/reconciler     event-driven sync: Traefik configs + /etc/hosts + certs
internal/render         Traefik dynamic + static config generation
internal/daemon         HTTP API over Unix socket + TCP + web dashboard
internal/client         Go client for the daemon API
internal/dns            /etc/hosts management + macOS /etc/resolver/ drop-in
internal/certs          mkcert integration for locally-trusted TLS
internal/platform       OS adapters: macOS LaunchAgent, Linux systemd
internal/cluster        provider-based cluster management (k3d, extensible)
internal/k3d            k3d-specific discovery and ingress watcher
internal/traefik        Traefik subprocess lifecycle management
internal/externaldns    external-dns webhook protocol implementation
pkg/types               shared domain types (Route)
pkg/config              project config parser (k8s resource API structure)
```

See [product_vision.md](product_vision.md) for the full design.
