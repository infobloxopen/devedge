# Contract: `Service` project-file schema + config package API

This feature is a CLI + library, so the contracts are (1) the `devedge.yaml` `Service` schema and
(2) the `pkg/config` public API.

## 1. `Service` YAML schema

```yaml
apiVersion: devedge.infoblox.dev/v1alpha1   # required; same group as Config
kind: Service                               # required
metadata:
  name: webhooks                            # required
spec:
  dev:
    hostname: webhooks.dev.test             # required; valid hostname
  dependencies:                             # optional; 0..N
    - name: db                              # required; unique within the service
      engine: postgres                      # required; one of: postgres | redis
      version: "16"                         # optional
      port: 5432                            # required; 1..65535
    - name: cache
      engine: redis
      port: 6379
  routes:                                   # optional; 0..N; same shape as Config routes
    - host: webhooks.dev.test
      upstream: http://127.0.0.1:8080
```

**Strict parsing**: unknown fields anywhere in a `Service` document are an error (typo
protection). This is `Service`-specific — `Config` documents keep their existing lenient parsing.

### Error contract (exit-time, actionable)

| Input | Result |
|-------|--------|
| `kind` not `Config`/`Service` | error names the unsupported kind and lists supported kinds |
| missing `apiVersion` / `kind` / `metadata.name` | error names the specific missing field |
| dependency missing `name`/`engine`/`port` | error names the dependency and the missing attribute |
| duplicate dependency `name` | error names the duplicate |
| `engine` not recognized | error lists recognized engines |
| `port` outside 1–65535 | error names the dependency and the bad port |
| `dev.hostname` empty/invalid | error describes the invalid hostname |
| unknown field (Service) | error names the unrecognized field |
| invalid YAML / empty file | clear parse error, no panic |

## 2. `pkg/config` public API

```go
// Polymorphic view over any project-file kind.
type Resource interface {
    Project() string
    ToRoutes() ([]types.Route, error)
}

// Optional: implemented by kinds that declare runtime dependencies.
type DependencyDeclarer interface {
    Dependencies() []Dependency
}

// Dispatch entry points (NEW). Read the apiVersion/kind envelope, then decode the
// matching kind. Config is decoded by the existing (lenient) ParseProject; Service is
// decoded strictly.
func ParseResource(data []byte) (Resource, error)
func LoadResource(path string) (Resource, error)

// EXISTING, unchanged — Config decoder and back-compat surface.
func ParseProject(data []byte) (*ProjectConfig, error)
func LoadProject(path string) (*ProjectConfig, error)
```

### CLI behavior contract (`de project up` / `de project down`)

- `project up` loads via `LoadResource`, registers `ToRoutes()` exactly as today.
  - If the resource declares dependencies, print the count + names and:
    `starting dependencies is not yet supported`.
  - If there are no routes, tell the user no routes were declared (not a silent success).
- `project down` loads via `LoadResource` and removes routes for `Project()`.
- A `Config` file produces identical behavior to before this feature.
