Think of it as **local ingress for development**: one small background service that owns ports 80 and 443 on your machine, gives every project stable FQDNs, terminates TLS with locally trusted certs, and forwards traffic to whatever is actually running underneath — a host process, Docker container, or a service behind k3d.

Traefik is a good foundation for this. It already supports dynamic configuration from multiple provider types, including files, Docker labels, and Kubernetes resources, and it exposes an API and dashboard that show the active routers and services. That makes it a strong runtime core for a tool whose main job is “accept registrations, reconfigure quickly, and show what is live.” ([Traefik Docs][1])

## Product description

**Devedge** is a local developer edge router and name registry.

It gives developers a predictable way to say:

* this project is `foo.dev.test`
* these sub-apps are `api.foo.dev.test`, `web.foo.dev.test`, `grafana.foo.dev.test`
* route all of them through one local edge on ports 80 and 443
* make HTTPS work without browser warnings
* let apps and local clusters register and deregister themselves automatically

The goal is to make local development feel closer to a real platform:

* hostnames instead of random ports
* one entry point instead of 15 ad hoc reverse proxies
* one TLS story instead of hand-made certs everywhere
* one way for projects to declare “here are my routes”

## Product vision

The product vision is:

**Every local development environment should have a stable, trusted, app-addressable edge.**

A developer should be able to install one tool, run one short command, and get:

* a local DNS experience
* trusted HTTPS
* automatic route registration
* shared 80/443 ownership
* good visibility into what is registered and why

It should work whether the app runs:

* directly on the host
* in Docker Compose
* in k3d
* in a hybrid setup where the edge is on the host and the services are behind a container network

## Positioning

Devedge is not trying to replace Kubernetes ingress controllers inside a cluster. It is the **host-local edge plane** for development.

In other words:

* **Traefik inside k3d** handles traffic once it reaches the cluster
* **Devedge on the host** handles developer-facing names, local TLS, and forwarding to the right place

That separation keeps the model clean.

## Core experience

The short CLI is `de`.

A typical developer experience would be:

```bash
brew install devedge
de install
de start
de init foo
de up
```

Then the project gets names like:

* `foo.dev.test`
* `web.foo.dev.test`
* `api.foo.dev.test`

And all of them resolve and terminate locally over HTTPS.

## Recommended hostname model

I would make the canonical suffix:

* `*.dev.test`

Why:

* `.test` is the reserved namespace intended for testing/private use
* you avoid `.local`, which conflicts with mDNS behavior
* you avoid dependency on a purchased public domain

A convenience alias can also be supported:

* `*.dev.localhost`

That is useful for quick bootstrap and some browser flows, but I would not make it the only story because not every client behaves uniformly with wildcard `localhost` names. The more durable product model is still a local resolver for `dev.test`. RFC 6761 reserves `localhost`, and RFC 6762 makes `.local` the mDNS namespace, which is why `.local` is the suffix to avoid for this design. ([GitHub][2])

## High-level architecture

Devedge has five parts.

### 1. `de` CLI

This is what developers and projects call.

Examples:

```bash
de install
de start
de stop
de ls
de doctor

de register web.foo.dev.test http://127.0.0.1:3000
de unregister web.foo.dev.test

de project up
de project down
```

### 2. `devedged` background service

This is the control plane daemon.

Responsibilities:

* own ports 80 and 443
* manage the route registry
* render Traefik dynamic config
* invoke mkcert for cert material
* monitor route leases and cleanup stale entries
* expose a local admin API and small web UI

### 3. Traefik runtime

Traefik is the data plane.

Responsibilities:

* accept HTTP and HTTPS traffic
* match by Host header / SNI
* forward to upstreams
* expose dashboard and raw route status
* reload when config changes

Traefik supports file-based configuration, Docker labels, and Kubernetes CRDs/providers, which is why it works well as the engine underneath Devedge rather than something Devedge has to reinvent. ([Traefik Docs][1])

### 4. Local DNS helper

This is what makes `*.dev.test` resolve to loopback.

Responsibilities:

* answer `A` and `AAAA` for `*.dev.test`
* map to `127.0.0.1` and `::1`
* optionally support search aliases and project-scoped names

This should be a tiny local DNS stub, not public DNS.

### 5. Certificate manager

This uses `mkcert`.

Responsibilities:

* install a local CA into system/browser trust stores
* mint wildcard or SAN-based certs for registered names
* rotate as needed
* never expose the CA private key

mkcert explicitly installs a local CA and generates locally trusted certificates, but it does not configure your servers for you, which is exactly the gap Devedge fills. mkcert also warns that the generated `rootCA-key.pem` is highly sensitive. ([GitHub][2])

## Why Traefik over Caddy here

Caddy would also work, but Traefik is slightly easier to justify for this product shape because Devedge needs:

* provider-driven dynamic config
* a route inventory API
* a dashboard
* future Docker and Kubernetes-aware integrations

Traefik already exposes route and service state through its API and dashboard, and it is built around provider-based discovery. That aligns well with a registry/control-plane product. ([Traefik Docs][3])

So the split becomes:

* **Devedge** = opinionated control plane, registry, installer, UX
* **Traefik** = edge runtime

## Install and setup model

I would make install explicitly one-time and privileged.

### First-time setup

```bash
de install
```

What it does:

1. installs `de` and `devedged`
2. installs Traefik as a bundled runtime or managed dependency
3. installs mkcert if needed
4. runs `mkcert -install`
5. installs the local DNS helper for `dev.test`
6. installs the daemon as a background service
7. reserves ports 80 and 443 for the daemon/runtime
8. prints a short health summary

### Day-to-day startup

```bash
de start
```

This ensures:

* daemon is running
* Traefik is live
* DNS helper is live
* cert store is present

### Diagnostics

```bash
de doctor
```

Checks:

* ports 80 and 443 available
* mkcert CA installed
* DNS resolution working for `foo.dev.test`
* Traefik reachable
* dashboard reachable
* cert generation works
* k3d adapter connectivity if enabled

## OS model

The install flow is where the platform-specific work happens.

The product should hide that behind `de install`.

### macOS

Install as a LaunchDaemon or LaunchAgent with the needed privileges for binding 80/443 and DNS configuration.

### Linux

Install as a systemd service. Either run as root with a minimized attack surface or give the runtime `CAP_NET_BIND_SERVICE` so it can bind 80/443 without full root.

### Windows

Install as a Windows service and configure the local DNS behavior through the product’s supported resolver path.

The important design point is that **the runtime is cross-platform, but install is platform-specific**. That is normal and acceptable.

## Major flows

## Flow 1: simple host process

Developer starts a web app on port 3000:

```bash
npm run dev
de register web.foo.dev.test http://127.0.0.1:3000
```

Devedge:

* records the route
* ensures cert coverage
* writes/updates Traefik dynamic config
* Traefik reloads
* route appears in dashboard
* browser hits `https://web.foo.dev.test`

## Flow 2: project-managed registration

A project has a `devedge.yaml`:

```yaml
apiVersion: devedge.infoblox.dev/v1alpha1
kind: DEConfig
metadata:
  name: foo
spec:
  routes:
    - host: web.foo.dev.test
      upstream: http://127.0.0.1:3000
    - host: api.foo.dev.test
      upstream: http://127.0.0.1:4000
```

Then:

```bash
de project up
de project down
```

`de project up` registers everything and starts lease heartbeats.
`de project down` removes them cleanly.

## Flow 3: Docker-aware registration

A container can carry metadata that Devedge reads, or Devedge can synthesize Traefik config from Docker metadata. Traefik itself can already discover routing config from Docker labels, so Devedge can either lean on that directly or normalize it into its own registry model. ([Traefik Docs][4])

I would prefer Devedge to keep its own registry and optionally import from Docker labels, because that gives one consistent user-facing model across host processes, Docker, and k3d.

## Flow 4: k3d-backed app

This is the most important one for your use case.

A project launches a k3d cluster and then wants local names to point into it.

### Recommended model

Expose the cluster ingress to a host port, then register routes in Devedge that forward there.

k3d explicitly recommends exposing services via ingress, and its docs show exposing the ingress controller’s port 80 from the load balancer to a host port like 8081. k3d also provides `host.k3d.internal`, which resolves inside the cluster to the host gateway and can be used by pods to reach host services like a local resolver. ([k3d.io][5])

That leads to a clean topology:

```text
browser
 -> https://api.foo.dev.test
 -> devedge on host :443
 -> Traefik on host
 -> http://127.0.0.1:8081   # k3d ingress exposed on host
 -> ingress inside k3d
 -> kubernetes service/pod
```

### How the app registers those resources

You have two good options.

### Option A: explicit registration from the launcher

The app that creates the k3d environment calls Devedge after cluster startup:

```bash
de register web.foo.dev.test http://127.0.0.1:8081 --project foo --path /
de register api.foo.dev.test http://127.0.0.1:8081 --project foo --path /
```

This works if the cluster ingress already knows how to route those Hosts internally.

This is the simplest and probably best v1.

### Option B: cluster agent / adapter

A small in-cluster or host-side adapter watches Kubernetes resources and mirrors selected ingresses into Devedge.

For example:

* watch `Ingress`
* optionally watch Traefik `IngressRoute`
* look for an opt-in annotation like `devedge.io/expose=true`
* register matching hosts in Devedge
* unregister when deleted

This is more automatic and nicer once the product matures.

## Registration model

I would make the route registry lease-based.

Every registration is a **lease**, not a forever object.

Example object:

```json
{
  "project": "foo",
  "host": "api.foo.dev.test",
  "upstream": "http://127.0.0.1:8081",
  "source": "k3d-adapter",
  "owner": "cluster:foo",
  "ttlSeconds": 30
}
```

### Why leases matter

They solve the local-dev stale state problem.

If a cluster dies, a terminal closes, or a launcher crashes, routes should disappear automatically.

### Lifecycle

* `register` creates or renews lease
* heartbeat refreshes lease
* `unregister` removes immediately
* daemon garbage-collects expired leases
* Traefik config is regenerated on every state change
* UI shows active, expiring, and stale routes

## How deregistration works

Three paths:

### Clean shutdown

The app or adapter calls:

```bash
de unregister api.foo.dev.test
```

### Project shutdown

The launcher calls:

```bash
de project down foo
```

This removes all leases for the project.

### Crash safety

If the owner disappears and heartbeats stop, the lease expires automatically.

That is the behavior I would trust most for dev environments.

## How Devedge notices and reconfigures

Internally, I’d make it event-driven.

### Inputs

* CLI calls
* local project manifests
* Docker events
* Kubernetes watch events from a k3d adapter
* lease expiration timers

### Internal loop

1. event arrives
2. registry is updated
3. desired route graph is recomputed
4. config renderer emits a new Traefik dynamic config bundle
5. config is atomically swapped
6. Traefik reloads
7. validation step checks Traefik API/rawdata
8. UI updates

Traefik’s API already exposes routers, services, entrypoints, and raw dynamic config state, so Devedge can use that to confirm that a reconfiguration actually took effect. ([Traefik Docs][3])

## How I would implement reconfiguration

For v1, use Traefik’s **file provider** and generate one file per route or project in a watched directory.

That keeps Devedge simple and inspectable:

```text
~/.devedge/traefik/dynamic/
  foo-web.yaml
  foo-api.yaml
  foo-grafana.yaml
```

Traefik supports file-based dynamic config, and file-based config is the easiest source for a local control plane to render deterministically. ([Traefik Docs][1])

Important detail: write files atomically. Render to temp, fsync, rename into place. That avoids partial reads and awkward reload edge cases.

## k3d consumption model in more detail

Here is the model I’d recommend for an app that launches a bunch of resources in k3d.

### App startup

1. launcher creates k3d cluster
2. cluster exposes ingress to host port 8081
3. app deploys its workloads and ingresses
4. launcher or adapter registers public dev hosts with Devedge
5. Devedge points those names at `http://127.0.0.1:8081`

### Internal cluster routing

Inside k3d, normal ingress routing still happens based on Host:

* `api.foo.dev.test` goes to API service
* `grafana.foo.dev.test` goes to Grafana
* `web.foo.dev.test` goes to frontend

### App shutdown

1. launcher deletes workloads or cluster
2. launcher calls `de project down foo`
3. remaining routes expire if anything was missed

This is clean because Devedge does not need to know pod IPs or join the cluster network. It only needs one stable host-visible upstream: the k3d ingress port mapping.

## CLI shape

I’d keep the CLI very short and biased toward project workflows.

### Core commands

```bash
de install
de start
de stop
de status
de doctor
de ui
de ls
```

### Route commands

```bash
de register HOST UPSTREAM
de unregister HOST
de renew HOST
de inspect HOST
```

### Project commands

```bash
de project init
de project up
de project down
de project ls
de project inspect foo
```

### k3d commands

```bash
de k3d attach foo --ingress http://127.0.0.1:8081
de k3d sync foo
de k3d detach foo
```

## Example project config

```yaml
apiVersion: devedge.infoblox.dev/v1alpha1
kind: DEConfig
metadata:
  name: foo
spec:
  defaults:
    ttl: 30s
    tls: true

  routes:
    - host: web.foo.dev.test
      upstream: http://127.0.0.1:3000

    - host: api.foo.dev.test
      upstream: http://127.0.0.1:8081
      mode: host-header-pass

    - host: grafana.foo.dev.test
      upstream: http://127.0.0.1:8081
      mode: host-header-pass
```

## UI

The Fly-based UI idea makes sense.

It should be tiny and focused.

Pages:

* active routes
* projects
* certs
* health
* recent events
* stale leases
* conflicts

A route detail page should show:

* host
* current upstream
* owner
* last heartbeat
* rendered Traefik object names
* certificate SAN coverage
* whether Traefik accepted it

## Conflict handling

This product needs a strong conflict story.

If two things try to claim the same host:

* first-writer wins by default
* second gets a clear conflict error
* UI shows who owns the name
* optional override flag for force-takeover

Without this, local environments get confusing fast.

## Technology choices

My picks would be:

* **CLI/service language:** Go
* **edge runtime:** Traefik
* **local certs:** mkcert
* **UI:** Fly
* **state store:** embedded SQLite or BoltDB
* **config format:** YAML
* **k8s integration:** client-go watcher or shell out only for v1
* **service install:** native per OS

Why Go:

* good fit for daemon + CLI
* easy cross-platform distribution
* strong ecosystem for networking and Kubernetes
* straightforward embedding of a small admin API

Why SQLite:

* route registry, leases, and event history are easy to model
* portable
* inspectable for debugging

## Recommended v1 scope

I would keep v1 intentionally narrow:

* host-local daemon
* ports 80 and 443
* `*.dev.test`
* mkcert integration
* Traefik file provider
* explicit registration via CLI and project file
* k3d support via exposed ingress port, not deep cluster discovery
* simple UI

That already solves most of the pain.

## Recommended v2 scope

Then add:

* Docker event import
* Kubernetes watch adapter
* automatic sync from Ingress / IngressRoute
* project templates
* route conflict policies
* path routing and middleware support

## One-sentence product pitch

**Devedge is a local development edge plane that gives every project stable HTTPS hostnames on one shared 80/443 entry point, and lets host apps, containers, and k3d clusters register routes dynamically.**

## Example “how it works” summary for docs

Install Devedge once. It sets up a local daemon, a local DNS helper for `*.dev.test`, and a locally trusted CA using mkcert. Projects then register routes with `de`, either explicitly or through a project file. Devedge writes Traefik dynamic config, Traefik begins serving the route on ports 80 and 443, and your browser reaches the correct service with a trusted certificate. For k3d-based apps, expose the cluster ingress to a host port and have Devedge forward matching hostnames to that ingress port while preserving the Host header. Traefik’s API and dashboard provide runtime visibility into which routes are active and where they point. ([Traefik Docs][3])

If you want, I can turn this into a proper README-style spec with sections like Overview, Install, Quick Start, Architecture, and a sample `devedge.yaml`.

[1]: https://doc.traefik.io/traefik/reference/install-configuration/providers/overview/ "Traefik Providers Documentation - Traefik"
[2]: https://github.com/FiloSottile/mkcert "GitHub - FiloSottile/mkcert: A simple zero-config tool to make locally trusted development certificates with any names you'd like. · GitHub"
[3]: https://doc.traefik.io/traefik/reference/install-configuration/api-dashboard/ "Traefik API & Dashboard Documentation - Traefik"
[4]: https://doc.traefik.io/traefik/reference/install-configuration/providers/docker/ "Traefik Docker Documentation - Traefik"
[5]: https://k3d.io/v5.1.0/usage/exposing_services/ "Exposing Services - k3d"

