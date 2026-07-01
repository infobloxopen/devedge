---
title: de register
---

> Generated from `de register --help`. Run `make docs-cli` to refresh.

## `de register`

```text
Register a route. For HTTP services, UPSTREAM is a URL like
http://127.0.0.1:3000. For TCP services (databases, gRPC, binary protocols),
use --protocol tcp and specify the backend as host:port.

One host can hold several HTTP routes distinguished by URL path prefix
(--path). A request is matched to the route with the longest matching prefix;
a route with no --path is the host's catch-all. Use --strip-prefix when the
backend serves paths without the prefix (e.g. an "/api" route to a gateway
that answers on "/v1/...").

Examples:
  de register api.foo.dev.test http://127.0.0.1:3000
  de register app.dev.test http://127.0.0.1:3000                       # shell (catch-all)
  de register app.dev.test http://127.0.0.1:8080 --path /api --strip-prefix
  de register postgres.foo.dev.test 127.0.0.1:5432 --protocol tcp
  de register redis.foo.dev.test 127.0.0.1:6379 --protocol tcp
  de register secure-db.foo.dev.test 127.0.0.1:5432 --protocol tcp --backend-tls

Usage:
  de register HOST UPSTREAM [flags]

Flags:
      --backend-tls       use TLS to connect to upstream
  -h, --help              help for register
      --owner string      owner identifier
      --path string       URL path prefix; empty is the host's catch-all
      --project string    project name
      --protocol string   routing protocol: http (default) or tcp
      --strip-prefix      strip --path from the request path before forwarding
      --ttl string        lease TTL (e.g. 30s)
```

