# Data Model: Service kind in devedge configuration

Phase 1 output. Types live in `pkg/config`. Existing types are marked; new types are the bulk of
the feature.

## Resource (interface, NEW)

The CLI's polymorphic view over any project-file kind.

```go
type Resource interface {
    Project() string                       // metadata.name
    ToRoutes() ([]types.Route, error)      // routes to register
}

type DependencyDeclarer interface {        // optional; Service implements, Config does not
    Dependencies() []Dependency
}
```

## ProjectConfig (EXISTING — `kind: Config`)

Unchanged. Already implements `Project()` and `ToRoutes()`, so it satisfies `Resource` with no
edits. Does **not** implement `DependencyDeclarer`.

## ServiceConfig (NEW — `kind: Service`)

| Field | YAML | Type | Notes |
|-------|------|------|-------|
| APIVersion | `apiVersion` | string | must equal `devedge.infoblox.dev/v1alpha1` |
| Kind | `kind` | string | must equal `Service` |
| Metadata | `metadata` | `ObjectMeta` (existing) | `metadata.name` required |
| Spec | `spec` | `ServiceSpec` | |

### ServiceSpec

| Field | YAML | Type | Notes |
|-------|------|------|-------|
| Dev | `dev` | `ServiceDev` | development surface |
| Dependencies | `dependencies` | `[]Dependency` | may be empty |
| Routes | `routes` | `[]RouteEntry` (existing) | may be empty; reuses the Config route entry shape |

### ServiceDev

| Field | YAML | Type | Notes |
|-------|------|------|-------|
| Hostname | `hostname` | string | non-empty, valid hostname |

### Dependency

| Field | YAML | Type | Required | Notes |
|-------|------|------|----------|-------|
| Name | `name` | string | yes | unique within the service |
| Engine | `engine` | string | yes | one of `postgres`, `redis` |
| Version | `version` | string | no | free-form (e.g. `"16"`) |
| Port | `port` | int | yes | 1–65535 |

## Validation rules (FR-005, FR-007)

Applied by `ServiceConfig.Validate()` after a **strict** decode (`KnownFields(true)`):

| Rule | Failure message names… |
|------|------------------------|
| `apiVersion` present & supported | the missing/invalid apiVersion |
| `kind` == `Service` | (dispatch guarantees this) |
| `metadata.name` non-empty | the missing field |
| each dependency has `name`, `engine`, `port` | the offending dependency + missing attribute |
| dependency `name`s unique | the duplicated name |
| dependency `engine` ∈ {postgres, redis} | the bad engine + the recognized set |
| dependency `port` ∈ 1–65535 | the dependency + the out-of-range port |
| `dev.hostname` non-empty & valid | the invalid hostname |
| no unknown fields (strict decode) | the unrecognized field (from `yaml.v3`) |

## Methods

- `ServiceConfig.Project() string` → `Metadata.Name`
- `ServiceConfig.ToRoutes() ([]types.Route, error)` → maps `Spec.Routes` to `types.Route`
  (`Source: "project-file"`, `Project: metadata.name`), mirroring `ProjectConfig.ToRoutes`.
- `ServiceConfig.Dependencies() []Dependency` → `Spec.Dependencies`

## Relationships

- A `ServiceConfig` has 0..N `Dependency` and 0..N `RouteEntry`.
- `Dependency` is declared here and **consumed by a later runtime feature**; this feature does not
  turn dependencies into routes or start them.
