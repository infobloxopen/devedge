---
title: de register
---

> Generated from `de register --help`. Run `make docs-cli` to refresh.

## `de register`

```text
Register a route. For HTTP services, UPSTREAM is a URL like
http://127.0.0.1:3000. For TCP services (databases, gRPC, binary protocols),
use --protocol tcp and specify the backend as host:port.

Examples:
  de register api.foo.dev.test http://127.0.0.1:3000
  de register postgres.foo.dev.test 127.0.0.1:5432 --protocol tcp
  de register redis.foo.dev.test 127.0.0.1:6379 --protocol tcp
  de register secure-db.foo.dev.test 127.0.0.1:5432 --protocol tcp --backend-tls

Usage:
  de register HOST UPSTREAM [flags]

Flags:
      --backend-tls       use TLS to connect to upstream
  -h, --help              help for register
      --owner string      owner identifier
      --project string    project name
      --protocol string   routing protocol: http (default) or tcp
      --ttl string        lease TTL (e.g. 30s)
```

