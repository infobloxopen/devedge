---
title: de k3d
---

> Generated from `de k3d --help`. Run `make docs-cli` to refresh.

## `de k3d`

```text
Alias for 'de cluster' (k3d provider)

Usage:
  de k3d [command]

Available Commands:
  attach      Register routes for a cluster's ingress
  bootstrap   Set up a cluster for seamless devedge integration
  create      Create a local cluster pre-configured for devedge
  delete      Delete a cluster and clean up devedge routes
  detach      Remove all routes for a cluster
  ls          List clusters
  watch       Watch Ingress objects and auto-register with devedge

Flags:
  -h, --help   help for k3d

Use "de k3d [command] --help" for more information about a command.
```

### `de k3d attach`

```text
Register routes for a cluster's ingress

Usage:
  de k3d attach CLUSTER [flags]

Flags:
  -h, --help             help for attach
      --host strings     hostnames to register (repeatable)
      --ingress string   ingress URL (auto-detected if omitted)
```

### `de k3d bootstrap`

```text
Install devedge CA, cert-manager issuer, and external-dns webhook.

Safety: validates the cluster is local (loopback API server, known context
name pattern). Use --force to bypass if you know what you're doing.

Usage:
  de k3d bootstrap CLUSTER [flags]

Flags:
      --force              bypass local-cluster safety checks (DANGEROUS)
  -h, --help               help for bootstrap
      --namespace string   namespace for CA secret (default "cert-manager")
```

### `de k3d create`

```text
Create a k3d cluster with ingress port mapping, then bootstrap
it with mkcert CA, cert-manager issuer, and external-dns webhook.

Safety: refuses to target non-local clusters unless --force is used.

Usage:
  de k3d create CLUSTER [flags]

Flags:
      --agents int     number of agent nodes
  -h, --help           help for create
      --image string   k3s image (default: k3d default)
      --port string    host port for ingress load balancer (default "8081")
```

### `de k3d delete`

```text
Delete a cluster and clean up devedge routes

Usage:
  de k3d delete CLUSTER [flags]

Flags:
  -h, --help   help for delete
```

### `de k3d detach`

```text
Remove all routes for a cluster

Usage:
  de k3d detach CLUSTER [flags]

Flags:
  -h, --help   help for detach
```

### `de k3d ls`

```text
List clusters

Usage:
  de k3d ls [flags]

Flags:
  -h, --help   help for ls
```

### `de k3d watch`

```text
Watch Kubernetes Ingress objects annotated with
devedge.io/expose=true and automatically register/deregister their
hostnames with the devedge daemon.

Usage:
  de k3d watch CLUSTER [flags]

Flags:
      --context string        kubectl context (default: provider-specific)
      --devedge-url string    devedge daemon URL
  -h, --help                  help for watch
      --ingress-port string   host port for ingress (auto-detected)
      --namespace string      namespace to watch (default: all)
```

