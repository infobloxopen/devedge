# Implementation Plan: DNS Resolution Through macOS Per-Domain Resolver Framework

**Branch**: `001-fix-dns-udp-bind` | **Date**: 2026-05-17 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/001-fix-dns-udp-bind/spec.md`

## Summary

The daemon advertises a per-domain DNS endpoint via `/etc/resolver/<domain>` but
runs no DNS server — `:15353` serves only the HTTP admin API. This plan
introduces an in-process authoritative DNS server (UDP + TCP, loopback only) for
the configured suffixes, served on a dedicated port so the existing HTTP API on
`:15353` is untouched. A platform-specific `SuffixSource` keeps the set of
authoritative suffixes in agreement with `/etc/resolver/` without requiring a
daemon restart. The doctor check is rebuilt to perform a real resolution probe
and to identify the DNS endpoint as the unresponsive component when it is down.
The `github.com/miekg/dns` library handles wire-format concerns (UDP truncation
signalling, EDNS, message parsing) that hand-rolling would put at risk.

## Technical Context

**Language/Version**: Go 1.25.5 (from `go.mod`)
**Primary Dependencies**:

- New: `github.com/miekg/dns` — wire-format DNS protocol, server primitives for
  both UDP and TCP transports. Justification (Constitution §Engineering
  Standards: External dependencies): the alternative is hand-rolling DNS
  message encode/decode, EDNS handling, and UDP truncation logic, each of
  which has well-known pitfalls (RFC 1035 / 6891 compliance) that would
  pull more maintenance cost than the library does. `miekg/dns` is the de
  facto standard Go DNS implementation, broadly used (CoreDNS, Caddy,
  external-dns), small surface area in our use, and stable.
- Existing: `github.com/spf13/cobra`, `gopkg.in/yaml.v3`, standard library
  `net`, `log/slog`, `context`.

**Storage**: No new persistent storage. The set of authoritative DNS suffixes
is derived at runtime from `/etc/resolver/` on macOS (file listing) and is
empty on other platforms. No JSON/SQL/state files added.

**Testing**:

- Unit: `go test ./...` — DNS handler request/response logic, suffix-set
  membership, suffix-source listing and diff detection.
- Integration (`test/integration/`): boot the daemon with `DEVEDGE_HOME`
  pointing at a tempdir and a synthetic suffix source, then send real DNS
  queries via `miekg/dns` client over UDP and TCP to the listening endpoint.
- End-to-end (`test/e2e/`): a macOS-only test that drives the full
  `de install` path — writes `/etc/resolver/<suffix>` via the same code
  path, starts the daemon, queries via `net.Resolver` (which on macOS
  routes through the system resolver framework). Gated behind a build tag
  + environment guard because it requires root to write `/etc/resolver/`.

**Target Platform**:

- macOS (primary; the bug occurs here and the fix is validated here).
- Linux and Windows: DNS server still binds (loopback only) but the
  platform `SuffixSource` returns an empty set, so the server answers
  REFUSED to all queries until/unless suffixes are configured by some
  future platform path. No regression on those platforms.

**Project Type**: CLI + local daemon (single-machine control plane).
This matches the existing structure: `cmd/de/` (CLI), `cmd/devedged/`
(daemon), `internal/` (core), `pkg/` (shared types).

**Performance Goals**:

- DNS endpoint p95 response latency under 5 ms on loopback (well under the
  100 ms system-resolver-layer budget in SC-006).
- DNS endpoint sustains the full mix of UDP + TCP queries from
  `mDNSResponder` plus user tooling (`dig`, `host`) without saturation.
  The expected load on a developer machine is well under 100 qps.
- Suffix-source poll runs at most once per 5 s and reads only directory
  entries (constant-time per file), so it contributes negligible CPU.

**Constraints**:

- Loopback-only binding. Out-of-scope: exposing the DNS endpoint on any
  non-loopback interface.
- Must not conflict with the existing HTTP API on `127.0.0.1:15353` over
  TCP. Resolved by binding the DNS endpoint on a dedicated port
  (`127.0.0.1:15354` proposed in research.md).
- Must not interfere with `/etc/hosts` for hostnames outside any
  configured suffix (FR-011).
- No additional privileged operations beyond what `de install` already
  performs (writing `/etc/resolver/<suffix>`).

**Scale/Scope**:

- Number of suffixes: 0–10 (typical: 1–3).
- Number of queries: bursty on cold cache, otherwise low (mDNSResponder
  caches aggressively).
- Code surface: roughly one new package (`internal/dnsserver`), a small
  platform-specific suffix-source file, modifications to
  `internal/dns/resolver_darwin.go` (port number), wire-up changes in
  `internal/daemon/server.go`, and a rewrite of `internal/platform/doctor.go`'s
  DNS checks.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | Notes |
|-----------|--------|-------|
| I. Edge-First Developer Experience | ✅ PASS | Restores the documented `de install` → working resolution path. No new manual setup added; setup remains idempotent. |
| II. Spec-Driven, Test-Driven Delivery | ✅ PASS | Spec exists at `specs/001-fix-dns-udp-bind/spec.md`. Tasks plan (next phase) will list test tasks before implementation tasks. |
| III. End-to-End Confidence Over Mocked Comfort | ✅ PASS | FR-012 mandates an end-to-end test. Plan includes `test/e2e/` coverage for the macOS resolver path; mocks limited to unit-level handler tests. |
| IV. Portable Core, Explicit Platform Adapters | ✅ PASS | DNS handler and suffix set are platform-agnostic Go code in `internal/dnsserver`. Platform-specific behavior (listing `/etc/resolver/`) lives behind a `SuffixSource` interface with `_darwin.go` / `_other.go` files mirroring the existing `internal/dns/resolver_*.go` pattern. |
| V. Safe Reconciliation and Observable Operations | ✅ PASS | Suffix-set updates are explicit (poll → diff → apply), logged with structured fields. Doctor reports actual resolution state, not configuration presence. DNS server emits structured logs for queries inside configured suffixes that fail. |

Engineering Standards & Quality Gates check:

- **Architecture**: New code is Go; new dependency (`miekg/dns`) is justified
  above against the External Dependencies bar.
- **Testing Pyramid**: Unit covers handler logic; integration covers daemon
  wiring + DNS-over-the-loopback; e2e covers macOS resolver framework path.
- **Performance & Reliability**: Goals listed in Technical Context; suffix
  poll loop is bounded with context cancellation; restart-safe by design (no
  in-memory state beyond suffix cache, which rebuilds on startup).
- **Security & Trust**: No new privileged operations; binds loopback only; no
  new secrets handled. Failure modes (port busy, suffix source unreadable)
  surface as actionable diagnostics, not silent recovery.

**Result**: No violations. No entries needed in Complexity Tracking.

## Project Structure

### Documentation (this feature)

```text
specs/001-fix-dns-udp-bind/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   ├── dns-protocol.md      # External: DNS-protocol behavior (what clients observe)
│   └── suffix-source.md     # Internal: SuffixSource adapter contract
├── checklists/
│   └── requirements.md  # Spec quality checklist
└── tasks.md             # Phase 2 output (NOT created by /speckit.plan)
```

### Source Code (repository root)

```text
cmd/
├── de/                          # CLI (touched: install path may update suffix list)
├── devedged/                    # Daemon binary (touched: wire DNS server)
└── devedge-dns-webhook/         # Existing external-dns webhook (unchanged)

internal/
├── certs/                       # (unchanged)
├── client/                      # (unchanged)
├── cluster/                     # (unchanged)
├── daemon/
│   └── server.go                # MODIFY: start DNS server; add WithDNSAddr option
├── dns/
│   ├── hosts.go                 # (unchanged) — /etc/hosts management
│   ├── resolver_darwin.go       # MODIFY: write the new DNS port in the drop-in
│   └── resolver_other.go        # (unchanged) — no-op on non-darwin
├── dnsserver/                   # NEW PACKAGE
│   ├── server.go                # Public Server type, Run(ctx), config
│   ├── handler.go               # miekg/dns Handler — wildcard answer logic
│   ├── handler_test.go          # Unit tests for handler responses
│   ├── suffixes.go              # Thread-safe authoritative-suffix set
│   ├── suffixes_test.go         # Unit tests for set membership / diff
│   ├── source.go                # SuffixSource interface + polling loop
│   ├── source_darwin.go         # Darwin SuffixSource: lists /etc/resolver/
│   ├── source_other.go          # Non-darwin SuffixSource: returns empty list
│   └── server_test.go           # Server lifecycle + UDP/TCP round-trip
├── externaldns/                 # (unchanged)
├── k3d/                         # (unchanged)
├── platform/
│   └── doctor.go                # MODIFY: rebuild DNS check; real probe; clearer messages
├── proxy/                       # (unchanged)
├── reconciler/                  # (unchanged)
├── registry/                    # (unchanged)
├── render/                      # (unchanged)
├── traefik/                     # (unchanged)
└── version/                     # (unchanged)

pkg/
└── types/                       # (unchanged)

test/
├── e2e/
│   └── resolver_macos_test.go   # NEW: macOS resolver framework e2e (build-tagged)
└── integration/
    ├── server_test.go           # (unchanged baseline)
    └── dnsserver_test.go        # NEW: daemon + DNS endpoint over UDP/TCP

go.mod                           # MODIFY: add github.com/miekg/dns
go.sum                           # MODIFY: tidy
```

**Structure Decision**: Single-project Go layout, matching the existing
repository. The new code lands in a new `internal/dnsserver` package (parallel
to the existing `internal/dns` package, which is purely about `/etc/hosts` and
the `/etc/resolver/` drop-in file). Keeping the new package separate keeps a
clean boundary: `internal/dns` continues to be about platform configuration
files, and `internal/dnsserver` is about answering DNS queries. Platform
specialization in `internal/dnsserver` mirrors the `_darwin.go` / `_other.go`
naming convention already used in `internal/dns`, satisfying Principle IV.

## Complexity Tracking

> No constitution violations; no entries.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| — | — | — |
