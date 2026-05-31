# Quickstart: declaring a service with `kind: Service`

Validates User Story 1 (route a declared service) and User Story 2 (declare + report
dependencies). Assumes devedge is built and the daemon is running (see the `build-run` skill).

## 1. Write a `Service` project file

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

## 2. Bring the project up

```sh
bin/de project up -f devedge.yaml
```

Expected output (shape):

```
registered webhooks.dev.test -> http://127.0.0.1:8080
1 dependency declared: db (postgres). Starting dependencies is not yet supported.
```

## 3. Confirm routing (US1)

```sh
bin/de ls                                  # webhooks.dev.test is active
curl -k https://webhooks.dev.test          # reaches the upstream over HTTPS
```

This is identical to what an equivalent `kind: Config` file would do — the `Service` kind loses
nothing on routing.

## 4. See validation catch mistakes (US2/US3)

```sh
# Unknown engine → rejected with the recognized set:
#   dependency "db": unrecognized engine "postgERS" (recognized: postgres, redis)
# Typo'd field (strict Service parsing) → rejected naming the field:
#   field "hostnam" not found in type config.ServiceDev
# Unsupported kind → rejected listing supported kinds:
#   unsupported kind "Deployment" (supported: Config, Service)
```

## 5. Tear down

```sh
bin/de project down
```

## Backward compatibility check (SC-003)

An existing `kind: Config` file behaves exactly as before — `project up`/`down` produce the same
result, with no dependency line and no strict-field rejection.
