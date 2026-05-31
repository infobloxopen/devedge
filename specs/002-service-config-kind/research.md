# Research: Service kind in devedge configuration

Phase 0 output. The spec had zero `[NEEDS CLARIFICATION]` markers and one resolved clarification,
so research is focused on confirming the technical approach against the existing codebase and
`yaml.v3` capabilities. No external research agents required.

## Decision 1 — Strict unknown-field detection for `Service`, lenient for `Config`

- **Decision**: Decode `Service` with `yaml.NewDecoder(r)` + `dec.KnownFields(true)`, which makes
  `yaml.v3` return an error on any field not present in the target struct. Keep `Config` on the
  existing `yaml.Unmarshal` (lenient) path via the untouched `ParseProject`.
- **Rationale**: Directly implements clarification A — `Service` authors get typo protection
  (FR-007); `Config` files that previously loaded with stray fields keep loading (FR-002). Uses a
  stdlib-adjacent capability already available in the in-use `yaml.v3`; no new dependency.
- **Alternatives considered**:
  - *Strict for both kinds* — rejected: changes `Config` behavior, violating FR-002.
  - *Hand-rolled field whitelist* — rejected: reinvents what `KnownFields(true)` already does.

## Decision 2 — Polymorphism via a small `Resource` interface + optional `DependencyDeclarer`

- **Decision**: Introduce `Resource { Project() string; ToRoutes() ([]types.Route, error) }`
  implemented by both `*ProjectConfig` and `*ServiceConfig`, and an optional
  `DependencyDeclarer { Dependencies() []Dependency }` implemented only by `*ServiceConfig`. The
  CLI consumes `Resource` and type-asserts for `DependencyDeclarer`.
- **Rationale**: Idiomatic Go; keeps route registration a single code path for both kinds
  (FR-008); makes future kinds additive (FR-010) since a new kind only needs to satisfy the
  interface and register a decoder case. The optional interface keeps `Config` free of
  dependency concepts.
- **Alternatives considered**:
  - *One mega-struct with a `kind` field and the union of all fields* — rejected: muddies the
    types, doesn't scale to future kinds, and would force `Config` to carry `Service` fields.
  - *Generics* — rejected: no benefit here; the set of kinds is small and closed at compile time.

## Decision 3 — Reuse `ParseProject` as the `Config` decoder

- **Decision**: `ParseResource`'s `Config` case calls the existing `ParseProject` unchanged.
- **Rationale**: Guarantees identical `Config` behavior (FR-002, SC-003) — the existing code and
  its tests are the back-compat oracle. No duplication.
- **Alternatives considered**: *Refactor `ParseProject` into the dispatch* — rejected for this
  feature: more churn, more back-compat risk, no benefit. Can revisit later.

## Decision 4 — Recognized engines and validation rules

- **Decision**: Recognized engines are `postgres` and `redis` (spec assumption). Validation:
  required `name`/`engine`/`port` per dependency; `engine` ∈ {postgres, redis}; `port` in
  1–65535; dependency `name`s unique; `dev.hostname` non-empty and a syntactically valid hostname
  consistent with existing route hostnames.
- **Rationale**: Matches the spec's bounded scope; the recognized set is centralized so it can
  expand in the later runtime feature. Errors name the specific problem (FR-005, SC-002).
- **Alternatives considered**: *Accept any engine string now* — rejected: weakens the contract
  the runtime feature will depend on, and SC-002 requires unrecognized engines to be rejected.

## Decision 5 — Hostname validation scope

- **Decision**: Validate `dev.hostname` with a minimal syntactic check (non-empty, valid DNS
  hostname characters/labels), consistent with how route hosts are treated today; do not
  introduce a new hostname scheme.
- **Rationale**: Spec assumption ("follows existing devedge hostname conventions"); avoids
  over-building. Keeps the feature description-only.
- **Alternatives considered**: *Full RFC 1123 + suffix policy enforcement* — deferred: more than
  the spec asks; would be gold-plating flagged by the QA scope gate.

## Open items carried to later features (not this feature)

- Starting/stopping/health-checking dependencies; migrations/seed; Redpanda/Kafka.
- Whether a dependency declaration implies a derived TCP route.
- Expanding the recognized engine set.
