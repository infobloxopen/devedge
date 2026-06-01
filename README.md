# devedge

A local development edge router and name registry.

Devedge gives every project stable HTTPS hostnames on one shared 80/443 entry
point, and lets host apps, containers, and k3d clusters register routes
dynamically.

## Install

```bash
brew tap infobloxopen/tap
brew install --cask infobloxopen/tap/devedge
```

Or build from source:

```bash
make build    # binaries in ./bin/
```

## Quick start

```bash
de install       # install daemon, mkcert CA, DNS config
de start         # start the background daemon
de doctor        # verify everything is healthy
```

## Integrating devedge into your project

### Option 1: Host process (no containers)

If your app runs directly on the host (e.g. `npm run dev`, `go run .`):

1. Add a `devedge.yaml` to your project root:

```yaml
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Config
metadata:
  name: myapp
spec:
  defaults:
    ttl: 30s
    tls: true
  routes:
    - host: myapp.dev.test
      upstream: http://127.0.0.1:3000
    - host: api.myapp.dev.test
      upstream: http://127.0.0.1:4000
```

2. Start your app, then register routes:

```bash
npm run dev &          # or whatever starts your app
de project up          # registers all routes from devedge.yaml
```

3. Open `https://myapp.dev.test` in a browser — trusted HTTPS, no port numbers.

4. When done:

```bash
de project down
```

For long-running sessions, use `de project up --watch` to keep leases alive with
automatic heartbeats.

### Option 2: Ad-hoc route registration

For quick one-off routes without a config file:

```bash
de register myapp.dev.test http://127.0.0.1:3000
de register api.myapp.dev.test http://127.0.0.1:4000 --project myapp

# When done
de unregister myapp.dev.test
# Or remove all routes for a project
de project down myapp
```

### Option 3: k3d cluster (Kubernetes)

If your project runs in a k3d cluster:

```bash
# Create a cluster with devedge integration pre-configured
de cluster create myapp

# Deploy your app normally
kubectl apply -f k8s/

# Option A: explicit route attachment
de cluster attach myapp \
  --host api.myapp.dev.test \
  --host web.myapp.dev.test

# Option B: auto-register from Ingress objects
# Annotate your Ingress with devedge.io/expose=true, then:
de cluster watch myapp
```

For full Kubernetes-native integration (cert-manager + external-dns), bootstrap
the cluster first:

```bash
de cluster bootstrap myapp
```

This installs the mkcert CA into the cluster so cert-manager can issue
locally-trusted certificates, and deploys an external-dns webhook that
automatically registers Ingress hostnames with devedge.

Your app's Ingress manifests work unchanged between local dev and production —
only the cluster-level issuer and DNS provider differ.

### Option 4: Non-HTTP services (databases, gRPC)

Devedge can proxy TCP services like databases with SNI-based TLS:

```yaml
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Config
metadata:
  name: myapp
spec:
  routes:
    - host: api.myapp.dev.test
      upstream: http://127.0.0.1:3000
    - host: postgres.myapp.dev.test
      upstream: 127.0.0.1:5432
      protocol: tcp
    - host: redis.myapp.dev.test
      upstream: 127.0.0.1:6379
      protocol: tcp
```

Or via CLI:

```bash
de register postgres.myapp.dev.test 127.0.0.1:5432 --protocol tcp
```

Connect with TLS-aware clients:

```bash
psql "host=postgres.myapp.dev.test sslmode=require"
```

### Example: multi-service project

A typical full-stack project config:

```yaml
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Config
metadata:
  name: datakit
  labels:
    team: platform
spec:
  defaults:
    ttl: 30s
    tls: true
  routes:
    - host: web.datakit.dev.test
      upstream: http://127.0.0.1:3000
    - host: api.datakit.dev.test
      upstream: http://127.0.0.1:8080
    - host: grpc.datakit.dev.test
      upstream: 127.0.0.1:50051
      protocol: tcp
    - host: postgres.datakit.dev.test
      upstream: 127.0.0.1:5432
      protocol: tcp
    - host: redis.datakit.dev.test
      upstream: 127.0.0.1:6379
      protocol: tcp
```

```bash
# Start all services, then:
de project up --watch

# Everything reachable via stable hostnames:
# https://web.datakit.dev.test
# https://api.datakit.dev.test
# psql "host=postgres.datakit.dev.test sslmode=require"
# redis-cli -h redis.datakit.dev.test --tls
```

## CLI reference

```
de install          Install daemon and configure the system
de start            Start the daemon
de stop             Stop the daemon
de doctor           Check system health
de status           Show daemon status
de ui               Open the web dashboard

de register HOST UPSTREAM [--project P] [--ttl 30s] [--protocol tcp] [--backend-tls]
de unregister HOST
de renew HOST
de ls [--json]
de inspect HOST

de project up [-f devedge.yaml] [--watch]
de project down [PROJECT] [--clean]
de project chart [-f devedge.yaml] [-o DIR]

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
    - host: db.foo.dev.test
      upstream: 127.0.0.1:5432
      protocol: tcp
```

### `Service` kind

In addition to `kind: Config`, devedge understands `kind: Service` — a service-oriented
project file that routes exactly like `Config` but also declares its development hostname and
runtime dependencies. Unlike `Config`, a `Service` document is parsed **strictly**: unknown
fields are rejected to catch typos.

```yaml
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: webhooks
spec:
  dev:
    hostname: webhooks.dev.test    # required; valid hostname
  dependencies:                    # optional; started on `de project up`
    - name: db
      engine: postgres             # postgres | redis
      version: "16"                # optional
      port: 5432                   # 1-65535
    - name: cache
      engine: redis
      port: 6379
  routes:                          # optional; same shape as Config routes
    - host: webhooks.dev.test
      upstream: http://127.0.0.1:8080
```

`de project up` registers the routes and **starts the declared dependencies**. The full schema and
error contract are documented in
[`specs/002-service-config-kind/contracts/service-config.md`](specs/002-service-config-kind/contracts/service-config.md).

#### Dependency runtime

When a `Service` declares dependencies, `de project up` makes them real and reachable (requires the
`helm`, `kubectl`, and `k3d` CLIs):

- **Shared instance per engine, isolated per service.** devedge runs one Postgres and one Redis in
  the dev cluster (installed via Helm) and gives each service its own database + credentials
  (Postgres) or ACL user + key namespace (Redis), so co-located services never see each other's data.
- **Connection by hotload DSN.** For each dependency devedge writes the real DSN to
  `~/.devedge/services/<service>/<dep>.dsn` (mode `0600`) and reports an **indirect** env var the app
  consumes — e.g. `DATABASE_URL=fsnotify://postgres/<path-to-file>` (the
  [`infobloxopen/hotload`](https://github.com/infobloxopen/hotload) pattern; the app reads the real
  DSN from the file and hot-reloads on change). The same shape is emitted for every engine
  (`REDIS_URL=fsnotify://redis/<path>`).

```bash
de project up               # starts deps, prints each env var + DSN file, then registers routes
de project down             # releases deps; KEEPS data by default
de project down --clean     # also drops this service's database/keys
de project chart -o ./chart # emit a Helm chart for the service + abstract dependency claims
```

Data persists across `down`/`up`; `--clean` drops only the requesting service's data, never the
shared instance. `de project chart` emits (does not deploy) a Helm chart expressing dependencies as
abstract claims, so the same declaration maps to a shared logical database in dev and a dedicated
instance in a real cluster. See
[`specs/003-dependency-runtime/`](specs/003-dependency-runtime/) for the design and contract.

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
