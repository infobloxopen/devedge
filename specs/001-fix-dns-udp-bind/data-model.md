# Data Model: DNS Resolution Through macOS Per-Domain Resolver Framework

**Feature**: 001-fix-dns-udp-bind | **Phase**: 1 (Design & Contracts)
**Date**: 2026-05-17

The feature introduces no persistent storage. The data shapes below are
in-memory runtime structures owned by the new `internal/dnsserver` package
and the existing `internal/dns` package (which continues to manage on-disk
configuration files). All structures are bounded in size by the number of
configured suffixes (typically 1–3, never more than ~10 in practice).

---

## Entity: ConfiguredSuffix

A DNS suffix the system is authoritative for. The set of currently
configured suffixes is what the DNS server uses to decide whether to answer
or refuse a query.

| Field | Type | Description |
|-------|------|-------------|
| `Name` | `string` | The fully-qualified DNS suffix in lowercase, no leading or trailing dot. Example: `"dev.test"`. |

### Validation rules

- MUST be non-empty.
- MUST be a syntactically valid DNS name: dot-separated labels, each label
  matching `[a-z0-9-]+` (no leading/trailing hyphen), total length ≤ 253
  octets.
- MUST be stored lowercased; comparisons against incoming query names use
  the canonical lowercased form.
- Duplicates are de-duplicated by the holding set (see `AuthoritativeSet`).

### Source

`ConfiguredSuffix` values are derived by the platform-specific
`SuffixSource` (see `contracts/suffix-source.md`). On macOS, each file in
`/etc/resolver/` contributes one `ConfiguredSuffix` whose `Name` equals
the file name. On other platforms, the set is empty for the current
scope of this feature.

### Lifecycle

`ConfiguredSuffix` values are immutable; updates to the configured set are
modeled as add/remove on `AuthoritativeSet` rather than mutating an
existing entry. There is no per-suffix metadata (TTL, ACLs, etc.) in this
feature; that is intentionally out of scope.

---

## Entity: AuthoritativeSet

A thread-safe set of `ConfiguredSuffix` values. The DNS handler consults
the set on every query to decide whether the query name falls inside an
authoritative suffix.

### State

- Internal representation: `map[string]struct{}` keyed by the canonical
  (lowercased) `Name`, protected by a `sync.RWMutex`.

### Operations

| Operation | Signature | Semantics |
|-----------|-----------|-----------|
| `Replace` | `Replace([]ConfiguredSuffix)` | Atomically replaces the entire set. Computes `added` and `removed` slices vs. the previous state and returns them for logging. |
| `Match` | `Match(queryName string) (ConfiguredSuffix, bool)` | Returns the longest configured suffix that the query name equals or is a subdomain of, and a boolean indicating whether any match was found. Case-insensitive. |
| `Snapshot` | `Snapshot() []ConfiguredSuffix` | Returns a sorted copy of the current set (for logging and the doctor probe). |

### Validation and edge cases

- `Replace` MUST be safe to call concurrently with `Match` and
  `Snapshot`. Readers see either the prior set or the new set in its
  entirety, never a partial set.
- `Match` MUST handle a trailing-dot query name (`foo.dev.test.` →
  matches `dev.test`) by canonicalizing before matching.
- `Match` MUST return the longest-suffix match if multiple suffixes
  could apply (e.g. with configured suffixes `dev.test` and
  `foo.dev.test`, a query for `bar.foo.dev.test` returns `foo.dev.test`).
  Longest-suffix matching is the conventional DNS delegation rule and
  matches `mDNSResponder`'s own behavior.
- Empty `AuthoritativeSet` is a valid state; `Match` returns `(_, false)`
  for every query. The DNS server responds REFUSED in that case.

### State transitions

The set has no internal state machine; it is purely a value updated via
`Replace`. The polling loop in the DNS server is responsible for calling
`Replace` whenever the platform `SuffixSource` returns a changed list.

---

## Entity: DNSEndpoint (runtime, not persisted)

The bound network endpoint serving DNS queries.

| Field | Type | Description |
|-------|------|-------------|
| `Addr` | `string` | The `host:port` the DNS endpoint binds. Default: `127.0.0.1:15354`. Overridable via `daemon.WithDNSAddr(addr)` for tests and for the e2e harness that may pick an ephemeral port. |
| `UDPServer` | `*dns.Server` | A `miekg/dns` server instance bound on `Addr` over UDP. |
| `TCPServer` | `*dns.Server` | A `miekg/dns` server instance bound on `Addr` over TCP. |

### Validation rules

- `Addr` MUST resolve to a loopback address (`127.0.0.0/8` or `::1`).
  Binding any non-loopback address is rejected at startup with a
  structured error. This is enforced even when the value is overridden
  via `WithDNSAddr`, because the security posture (loopback only) is a
  spec-level constraint.

### Lifecycle

```
[constructed] --Run(ctx)--> [bound-udp+tcp]
                                |
                       ctx.Done() or fatal error
                                |
                                v
                          [shut down]
```

- `Run(ctx)` blocks until `ctx` is cancelled or both servers exit. On
  cancellation it calls `Shutdown` on both servers (with a 2 s timeout
  budget) and returns the first non-nil error from either, or nil.
- A bind error on either UDP or TCP causes `Run` to return that error
  without attempting to keep the other transport alive. Symmetry is the
  spec contract (FR-002).

---

## Entity: ResolverDropIn (file, existing)

Already managed by `internal/dns/resolver_darwin.go`. The schema is the
content of `/etc/resolver/<suffix>`:

```
# Managed by devedge — do not edit
nameserver 127.0.0.1
port <DNSEndpointPort>
```

### What changes in this feature

- The hardcoded `port 15353` literal becomes `port 15354` (or whatever
  port the daemon was started with, exposed as a build-time constant
  alongside the DNS endpoint default).
- The drop-in continues to be written by `de install` calling
  `dns.InstallResolverConfig(suffix)`; no change to call sites.

### Why this is data-model relevant

The drop-in is read on the runtime side by the macOS `SuffixSource`,
which lists the parent directory; the drop-in's *content* is not parsed
by the daemon. The only consumer of the content is `mDNSResponder` (the
OS itself).

---

## Relationships

```
ConfiguredSuffix ── (member of) ──> AuthoritativeSet
                                        ▲
                                        │ replaces atomically
                                        │
                                  SuffixSource (interface)
                                        ▲
                ┌───────────────────────┼────────────────────────┐
                │                       │                        │
       darwinSuffixSource       otherSuffixSource         (test injected)
       (lists /etc/resolver/)   (returns empty set)       (configurable)

DNSEndpoint ── consults ──> AuthoritativeSet ── filters ──> incoming queries
```

The `SuffixSource` adapter is the only place platform specialization
lives. Everything downstream of `AuthoritativeSet` is platform-agnostic
core (Principle IV).

---

## Storage and persistence

- **No new persistent storage.** Suffixes are derived from
  `/etc/resolver/` on macOS at runtime. The route registry, hosts file
  management, and other persisted state are untouched.
- **No schema migrations.** No on-disk formats change. The only
  externally-visible content change is the port number written into
  `/etc/resolver/<suffix>`, which is regenerated by `de install`.

---

## Out of scope for this feature

- Per-route DNS records (each name in the suffix resolves to the same
  `EdgeIP`; per-name distinct addresses are not modeled).
- IPv6 answers (`AAAA` returns NODATA).
- DNSSEC, zone transfers, dynamic update (RFC 2136).
- Authority/SOA records.
- Recursive resolution.
- Caching of upstream answers.

These are explicit non-goals per the spec's Out of Scope section and are
deliberately not modeled here so that the data model does not
preemptively shape future features.
