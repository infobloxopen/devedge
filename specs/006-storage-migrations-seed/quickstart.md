# Quickstart: Schema migrations and dev seed on `up`

How a developer uses feature 006 once shipped. Assumes devedge installed and a project with a Postgres
dependency (003) — optionally deployed in-cluster (005).

## 1. Add migrations to the project

```
my-service/
├── devedge.yaml
└── db/
    ├── migrations/
    │   ├── 0001_create_widgets.up.sql
    │   ├── 0001_create_widgets.down.sql
    │   └── 0002_add_widgets_color.up.sql
    │   └── 0002_add_widgets_color.down.sql
    └── seed/
        └── dev.sql          # optional, local/dev only
```

## 2. Declare them in `devedge.yaml`

```yaml
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata: { name: widgets }
spec:
  dev: { hostname: widgets.dev.test }
  dependencies:
    - name: db
      engine: postgres
      migrations: db/migrations
      seed: db/seed/dev.sql
```

## 3. Local-run: `de project up`

```
$ de project up
✔ cluster: devedge (shared)
✔ dependency db (postgres): provisioned
✔ db: migrated 0 → 2 (2 applied)
✔ db: seeded (dev.sql)
DATABASE_URL=fsnotify:///…/services/widgets/db.dsn
```

Your process now connects to a database that already has the schema and seed data. Re-running is a no-op:

```
$ de project up
✔ db: schema already current (v2)
✔ db: already seeded
```

## 4. In-cluster deploy: `de project up --deploy`

Requires the service image to expose a `migrate` subcommand (see `contracts/` C2). Migrations run as a
Helm `pre-install`/`pre-upgrade` hook Job **before** the workload rolls:

```
$ de project up --deploy
✔ dependency db (postgres): provisioned
✔ migrate job: 0 → 2 (pre-upgrade hook)
✔ workload widgets: rolled out (v2 schema)
✔ https://widgets.dev.test
```

## 5. CI: `de ci run -- de project up`

On the ephemeral CI cluster, **migrations run, seed does not** (tests own their fixtures):

```
$ de ci run -- de project up
✔ db: migrated 0 → 2
✔ db: seed skipped (CI)
```

## 6. Rollback (down migrations survive image changes)

Because applied down steps are persisted at apply time, you can reverse the schema even from an older
image that no longer ships those `.down.sql` files (FR-012):

```
$ de project up --deploy          # deploys image with schema v2
# … later, roll back to the v1 image …
$ de project up --deploy          # hook reverses v2 using the persisted down step
✔ migrate job: 2 → 1
```

## 7. Reset

```
$ de project down            # preserves schema + data
$ de project down --clean    # drops the DB → next `up` rebuilds schema and re-seeds
```

## Verifying the change (maps to acceptance)

- **US1/SC-001/002**: `up` creates the tables; re-`up` makes no change. `psql … \dt` shows the schema.
- **US2/SC-003**: a workload querying a migrated table succeeds on first request, local-run and `--deploy`.
- **US3/SC-005**: seeded rows present; `down --clean` then `up` rebuilds them.
- **SC-004/FR-007**: a deliberately bad migration aborts `up` with a clear message; fixing it and re-running
  recovers with no manual `psql` cleanup.
- **SC-007/FR-012**: reverse a migration using an image that lacks the down file; it still succeeds.

e2e lives in `test/e2e/` (run with `DEVEDGE_E2E=1 go test ./test/e2e/...`), modeled on
`dependency_postgres_test.go` (local-run) and `workload_deploy_test.go` (deploy hook).
