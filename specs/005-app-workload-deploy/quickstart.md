# Quickstart: Deploy the app workload onto the resolved cluster

Run your service *inside* the resolved cluster, next to its dependencies — opt-in, one command.

## Declare a workload

Add a `workload` block to your `kind: Service` `devedge.yaml`. Use a pre-built image:

```yaml
apiVersion: devedge.infoblox.dev/v1alpha1
kind: Service
metadata:
  name: my-svc
spec:
  dev:
    hostname: my-svc.dev.test
  workload:
    image: ghcr.io/acme/my-svc:dev
    port: 8080
  dependencies:
    - name: db
      engine: postgres
      port: 5432
```

…or build from the project (no registry needed — the image is loaded straight into the cluster):

```yaml
  workload:
    build:
      context: .
      # dockerfile: Dockerfile   # optional, defaults to Dockerfile
    port: 8080
```

## Deploy (developer)

```bash
de project up --deploy
# cluster: devedge (shared dev)
# dependency db (postgres) ready
# deployed: my-svc -> cluster devedge (1 replica), https://my-svc.dev.test
```

What happened:
1. The cluster was resolved + ensured (shared `devedge`), and the `db` dependency provisioned (003/004).
2. The image was used as declared, or built and `k3d image import`ed into the cluster.
3. The `service` chart was installed (`helm upgrade --install --wait`): Deployment + Service + Ingress.
4. The workload connects to `db` over the in-cluster Service DNS using its per-service credentials.
5. The dev hostname routes to the workload via devedge's ingress-watch path.

Reach it over its stable hostname:

```bash
curl https://my-svc.dev.test/healthz
```

Without `--deploy`, `de project up` behaves exactly as before (local-run + deps) — deploy never changes
the default inner loop.

Re-deploy after a change (idempotent — rolls out, no duplicate):

```bash
de project up --deploy
```

Tear down the workload (footprint-only — never the shared cluster or another project):

```bash
de project down            # removes the workload (+ routes); keeps dependency data
de project down --clean    # also drops this service's dependency data
```

## Deploy (CI, ephemeral cluster)

```bash
de ci run -- de project up --deploy   # deploys onto the per-run ephemeral cluster, torn down on exit
```

## Notes

- Deploy is **opt-in**: a service's dev hostname resolves to exactly one running instance at a time
  (local **or** in-cluster). Don't run the same service locally and deployed for the same hostname.
- Build path requires a container build tool (e.g. Docker) on PATH; no external registry is required.
- Co-located deployed services on the shared cluster are isolated per service and don't interfere.
