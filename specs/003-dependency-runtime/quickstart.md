# Quickstart: Dependency runtime for the Service kind

Goal: declare a Postgres dependency, bring it up, and connect — without creating a database, a
user, or touching the cluster.

## 1. Declare the service and its dependency

`devedge.yaml`:

```yaml
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: webhooks
spec:
  dev:
    hostname: webhooks.dev.test
  dependencies:
    - name: db
      engine: postgres
      version: "16"
      port: 5432
  routes:
    - host: webhooks.dev.test
      upstream: http://127.0.0.1:8080
```

## 2. Bring it up

```bash
de project up
```

devedge ensures the shared Postgres is running (Helm), provisions an isolated database + credentials
for `webhooks`, waits until it accepts connections, registers the route, and prints something like:

```
route  webhooks.dev.test → http://127.0.0.1:8080   registered
dep    db (postgres)      ready
       DATABASE_URL=fsnotify://postgres/Users/me/.devedge/services/webhooks/db.dsn
       real DSN written to ~/.devedge/services/webhooks/db.dsn (0600)
```

## 3. Connect from your service (Postgres / hotload)

```go
import (
    "database/sql"
    _ "github.com/infobloxopen/hotload"
    _ "github.com/infobloxopen/hotload/fsnotify"
)

db, err := sql.Open("hotload", os.Getenv("DATABASE_URL"))
// real DSN is read from the referenced file; credential changes hot-reload, no restart.
```

The same env-var shape is emitted for Redis (`REDIS_URL=fsnotify://redis/<path>`); a Redis client
reads the referenced file (reload is the app's concern — the pattern is uniform across engines).

## 4. Data persists across restarts; wipe explicitly

```bash
de project down          # keeps your data (default, non-destructive)
de project up            # your schema + rows are still there

de project down --clean  # drops ONLY this service's database/role; never the shared instance
```

## 5. Get a deployable chart (no Kubernetes authoring)

```bash
de project chart -o ./chart   # emits a Helm chart for the service + abstract dependency claims
helm lint ./chart             # passes
```

The chart expresses dependencies abstractly — a shared logical DB in dev, a dedicated instance via
the org DB abstraction in a real cluster — using the same `fsnotify://…` env-var shape (file backed
by a mounted secret).

## Notes / prerequisites

- Requires the `helm`, `kubectl`, and `k3d` CLIs on PATH and a running dev cluster.
- Two services that both declare a dependency named `db` are isolated automatically (separate
  databases + credentials in the one shared Postgres); neither sees the other's data.
