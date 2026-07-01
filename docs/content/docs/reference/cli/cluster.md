---
title: de cluster
---

> Generated from `de cluster --help`. Run `make docs-cli` to refresh.

## `de cluster`

```text
Manage local Kubernetes clusters with devedge integration

Usage:
  de cluster [command]

Available Commands:
  attach      Register routes for a cluster's ingress
  bootstrap   Set up a cluster for seamless devedge integration
  create      Create a local cluster pre-configured for devedge
  delete      Delete a cluster and clean up devedge routes
  detach      Remove all routes for a cluster
  ls          List clusters
  watch       Watch Ingress objects and auto-register with devedge

Flags:
  -h, --help   help for cluster

Use "de cluster [command] --help" for more information about a command.
```

### `de cluster attach`

```text
Register routes for a cluster's ingress

Usage:
  de cluster attach CLUSTER [flags]

Flags:
  -h, --help             help for attach
      --host strings     hostnames to register (repeatable)
      --ingress string   ingress URL (auto-detected if omitted)
```

### `de cluster bootstrap`

```text
Install devedge CA, cert-manager issuer, and external-dns webhook.

Safety: validates the cluster is local (loopback API server, known context
name pattern). Use --force to bypass if you know what you're doing.

Usage:
  de cluster bootstrap CLUSTER [flags]

Flags:
      --force              bypass local-cluster safety checks (DANGEROUS)
  -h, --help               help for bootstrap
      --namespace string   namespace for CA secret (default "cert-manager")
```

### `de cluster create`

```text
Create a k3d cluster with ingress port mapping, then bootstrap
it with mkcert CA, cert-manager issuer, and external-dns webhook.

Safety: refuses to target non-local clusters unless --force is used.

Usage:
  de cluster create CLUSTER [flags]

Flags:
      --agents int     number of agent nodes
  -h, --help           help for create
      --image string   k3s image (default: k3d default)
      --port string    host port for ingress load balancer (default "8081")
```

### `de cluster delete`

```text
Delete a cluster and clean up devedge routes

Usage:
  de cluster delete CLUSTER [flags]

Flags:
  -h, --help   help for delete
```

### `de cluster detach`

```text
Remove all routes for a cluster

Usage:
  de cluster detach CLUSTER [flags]

Flags:
  -h, --help   help for detach
```

### `de cluster ls`

```text
List clusters

Usage:
  de cluster ls [flags]

Flags:
  -h, --help   help for ls
```

### `de cluster watch`

```text
Watch Kubernetes Ingress objects annotated with
devedge.io/expose=true and automatically register/deregister their
hostnames with the devedge daemon.

Usage:
  de cluster watch CLUSTER [flags]

Flags:
      --context string        kubectl context (default: provider-specific)
      --devedge-url string    devedge daemon URL
  -h, --help                  help for watch
      --ingress-port string   host port for ingress (auto-detected)
      --namespace string      namespace to watch (default: all)
```

