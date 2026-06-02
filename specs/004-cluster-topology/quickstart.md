# Quickstart: Cluster topology

How the shared-dev / ephemeral-CI / dedicated-cluster model behaves once this feature lands.

## Developer: just run up

```bash
# No cluster yet, no kube-context fiddling:
de project up
# → ensures the shared dev cluster once:
#     Ensuring shared dev cluster 'devedge'... created + bootstrapped (first time only)
#     cluster: devedge (shared dev)
#     dependency db (postgres) ready
#       DATABASE_URL=fsnotify://...   real DSN written to ~/.devedge/.../db.dsn

# A second project, later — lands on the SAME cluster, no second create:
cd ../other-svc && de project up
# → cluster: devedge (shared dev)
```

Two services using the conventional dependency name `db` coexist: each gets its own isolated
database + credentials inside the shared Postgres (feature 003), so neither sees the other's data.
Your current `kubectl` context is never changed.

```bash
de project down            # releases this project's deps + routes; data kept; others untouched
de project down --clean    # also drops THIS project's dependency data
```

## CI: ephemeral cluster per run

```yaml
# .github/workflows/ci.yml (illustrative)
- run: de ci run -- go test ./test/e2e/...
#   de ci run:
#     • forces ephemeral mode unconditionally (does not rely on CI=true)
#     • creates + bootstraps devedge-ci-$GITHUB_RUN_ID
#     • runs the wrapped command with DEVEDGE_KUBECONTEXT set
#     • tears the cluster down on exit — pass, fail, or cancel
#   The workflow never calls `k3d` directly.
```

Concurrent runs get distinctly named clusters (`devedge-ci-<runid>`) and never interfere. The same
e2e suite that runs here also passes against the shared `devedge` cluster locally — tests use
project-unique names and clean up after themselves.

## A project that can't coexist: dedicated cluster

```yaml
# devedge.yaml
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata: { name: heavy-svc }
spec:
  dev: { hostname: heavy-svc.dev.test }
  cluster:
    dedicated: true        # gets its own cluster instead of the shared one
  dependencies:
    - { name: db, engine: postgres, port: 5432 }
```

```bash
de project up
# → cluster: devedge-proj-heavy-svc (dedicated)
# A co-located project without the opt-in still lands on 'devedge' (shared dev).
```

## Rare exception: a dedicated dependency instance

```yaml
  dependencies:
    - name: db
      engine: postgres
      port: 5432
      dedicated: true       # own Postgres instance instead of the shared per-engine one
```

Use this only when per-service logical isolation inside the shared instance isn't enough but a whole
separate cluster is overkill. For full isolation, prefer `cluster.dedicated: true`.

## What did NOT change

- Feature 003's dependency contract: hotload DSN env var + real-DSN file, persistence across
  `down`/`up`, `--clean`, and chart generation — all identical, just on the resolved cluster.
- The explicit `de cluster create|bootstrap|attach|watch ...` commands still work for hand-managed
  clusters.
