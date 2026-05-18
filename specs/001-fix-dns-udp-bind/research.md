# Research: DNS Resolution Through macOS Per-Domain Resolver Framework

**Feature**: 001-fix-dns-udp-bind | **Phase**: 0 (Outline & Research)
**Date**: 2026-05-17

This document resolves the open design questions identified in the plan's
Technical Context. There are no `NEEDS CLARIFICATION` markers from the spec
to resolve; all entries here are technology-choice and integration-pattern
decisions required before Phase 1 design.

---

## R1. DNS protocol library: hand-rolled vs `github.com/miekg/dns`

**Decision**: Use `github.com/miekg/dns`.

**Rationale**:

- Wire-format DNS has multiple subtleties that have historically tripped up
  hand-rolled servers: UDP message size limits and the truncation (`TC`) bit
  signalling for TCP retry (RFC 1035 §4.2.1), EDNS(0) OPT pseudo-records and
  payload-size negotiation (RFC 6891), and case-insensitive name comparison
  with 0x20-encoding interaction (RFC 1035 §2.3.3 / draft-vixie-dnsext-dns0x20).
  Each of these is a correctness landmine that the spec touches: FR-002
  (UDP + TCP equivalence), FR-005 (well-formed empty answers), and the
  "responses exceeding UDP-safe payload size" edge case all bake in
  protocol-correctness expectations.
- `miekg/dns` is the canonical Go DNS implementation (used by CoreDNS,
  ExternalDNS, Caddy, k8s coredns plugins). It is small (no transitive deps
  beyond `golang.org/x/*`), maintained, and stable.
- The constitution's External Dependencies bar requires justification by
  "maintenance cost, portability, and operational value." Reimplementing
  RFC 1035 / 6891 by hand would push maintenance cost in the wrong direction
  for no portability or operational gain.

**Alternatives considered**:

- **Hand-roll a minimal DNS parser/encoder**. Rejected — too easy to get
  edge cases wrong, and the project has explicit testing-pyramid
  expectations (Principle III) that would force us to recreate substantial
  portions of `miekg/dns`'s own test surface.
- **Use only `net.LookupHost`-style helpers from the standard library**.
  Not applicable — these are *client* primitives. There is no DNS server
  in the standard library.

---

## R2. Where to bind the DNS endpoint: same port as HTTP, dedicated port, or UDP-only on the shared port?

**Decision**: Bind the DNS endpoint on a **dedicated port** at
`127.0.0.1:15354` for both UDP and TCP. Leave the existing HTTP admin API
on `127.0.0.1:15353` untouched. Rewrite `/etc/resolver/<suffix>` to point at
port 15354.

**Rationale**:

- The shared-port-UDP-only approach (bind UDP `:15353`, leave HTTP on TCP
  `:15353` as today) was the most literal reading of the issue's suggested
  fix. It works for the common UDP path. But macOS *will* fall back to TCP
  for truncated UDP responses (over the EDNS-negotiated payload size or
  the 512-byte legacy limit, RFC 5966) and for some platform queries. On
  TCP fallback, the DNS client would connect to our HTTP server and
  receive an HTTP error response framed as DNS bytes, producing undefined
  client behavior. Principle V (Safe Reconciliation): "when the system
  cannot guarantee correctness, it MUST surface a clear error." We
  cannot guarantee correctness with this hybrid.
- Moving the HTTP API off `:15353` (so DNS gets UDP+TCP at the historical
  port) was considered. Rejected: there are external references to the
  HTTP API on `:15353` — notably the in-cluster external-dns webhook
  (`http://host.k3d.internal:15353` in `internal/k3d/bootstrap.go`) and
  the `devedge-dns-webhook` binary's default flag. The blast radius is
  larger than necessary, and the spec explicitly notes the port choice is
  flexible. Better to keep the well-known HTTP port stable.
- A dedicated DNS port keeps the architecture honest: the HTTP API stays
  exactly as before (no contract change), the DNS endpoint is its own
  thing with both transports, and the only place that hardcodes the DNS
  port is `internal/dns/resolver_darwin.go`, which is rewritten in this
  feature anyway.
- Choice of `15354` specifically: adjacent to the existing `15353`,
  unprivileged, not in IANA's well-known ranges. Easy to remember,
  trivially audited via `lsof` ("the two 1535x ports are devedge").

**Alternatives considered**:

- `127.0.0.1:15353` UDP + TCP DNS, HTTP moved to `:15355` or unix-socket
  only. Rejected on blast-radius grounds (above).
- Port 53. Rejected: privileged, requires elevated capabilities, conflicts
  with `mDNSResponder` listening on `*:53` on macOS.
- Random ephemeral port written into the resolver drop-in at install
  time. Rejected: complicates `lsof`-style diagnosis and the doctor
  probe; provides no real benefit over a fixed unprivileged port.

---

## R3. Wildcard answer semantics for queries inside a configured suffix

**Decision**: For queries whose name equals or is a subdomain of a configured
suffix, the DNS server answers:

- `A` (IPv4) queries: a single A record pointing to the local edge IP
  (`pkg/types.EdgeIP`, currently `127.0.0.2`), with TTL **60 s**.
- `AAAA`, `MX`, `TXT`, `SRV`, and any other types: NOERROR with an empty
  answer section (NODATA). No SOA in the authority section (we are not a
  conventional zone-style authority; clients should not treat absent
  records as authoritatively negative for caching).
- Queries for names outside every configured suffix: REFUSED. In practice
  these should not reach the endpoint, because macOS's per-domain
  resolver framework only routes queries matching `/etc/resolver/<suffix>`
  here, but REFUSED is the correct posture for any that do arrive (e.g.
  the user manually pointing `dig` at the endpoint).

**Rationale**:

- The spec's User Story 3 and the design comment in
  `internal/dns/resolver_darwin.go` are explicit that wildcard semantics
  are the goal: any name in the suffix → loopback. This is also the only
  semantic that makes the per-domain resolver path more valuable than
  `/etc/hosts`, since hosts entries are inherently exact-match.
- IPv4-only with NODATA for `AAAA` matches existing system behavior:
  `pkg/types.EdgeIP` is an IPv4 loopback alias, and the existing
  `checkDNS` doctor function accepts `127.0.0.1`, `127.0.0.2`, or `::1`
  as success. Returning NODATA for `AAAA` lets clients fall through to
  the `A` record without a synthetic IPv6 answer. (If/when the edge
  binds an IPv6 loopback, this becomes a small follow-up; out of scope
  here per spec Assumptions.)
- TTL of 60 s balances responsiveness against unnecessary repeat queries.
  Local developer state changes are infrequent at minute scale; 60 s is
  long enough to suppress repeated lookups by browsers and short enough
  that operator changes propagate within the next minute.
- REFUSED (vs SERVFAIL) for off-suffix queries: REFUSED is the
  conventional "not my zone" response that prompts the resolver
  framework to try the next configured resolver / upstream. SERVFAIL
  would imply transient failure and tempt clients to retry against us.

**Alternatives considered**:

- Answer only for registered hostnames (no wildcard). Rejected: removes
  the differentiating value over `/etc/hosts`, contradicting spec User
  Story 3.
- Synthesize an IPv6 answer pointing at `::1`. Rejected: the edge proxy
  binds the IPv4 alias `127.0.0.2`; returning `::1` would route browser
  traffic to a different stack and lose the SNI/host-routing properties
  of the configured edge. Best to be honest with NODATA.
- Include SOA in the authority section. Rejected: implies we are an
  authoritative zone server with negative-cache semantics. We are not.
  Bare NOERROR with empty answer is simpler and avoids long negative
  caches if a name later does need to resolve.

---

## R4. How does the running DNS server learn the current set of authoritative suffixes?

**Decision**: Treat `/etc/resolver/` (on macOS) as the authoritative source.
On startup and at a bounded polling interval, the DNS server's macOS
`SuffixSource` lists `/etc/resolver/` and reports the file names as the
current suffix set. On non-macOS platforms, the source returns the empty
set. The server diff-applies the result: added suffixes are logged, removed
suffixes are logged, and the in-memory authoritative set is updated
atomically.

**Polling interval**: **5 seconds**. Lower bound is set by the spec's
"without requiring a daemon restart" requirement (FR-008) and by the
typical interactive feedback expectation of `de install`. Five seconds
keeps post-install propagation human-imperceptible while contributing
effectively zero CPU (a single `os.ReadDir` per tick).

**Rationale**:

- The macOS resolver-framework path is itself driven by the contents of
  `/etc/resolver/`. Using that directory as the source of truth means
  there is exactly one configuration surface; the DNS server is never out
  of sync with what `mDNSResponder` thinks is delegated to it.
- This avoids adding any new API surface on the HTTP daemon. `de install`
  continues to call `dns.InstallResolverConfig(domain)` exactly as today
  — the daemon picks up the change on the next poll, without any
  coordination protocol between the CLI and the daemon process.
- Polling beats `fsnotify`-style file watching for two reasons. First,
  `fsnotify` adds a transitive dependency we do not otherwise need.
  Second, file watching on `/etc/resolver/` requires correctly handling
  the various ways macOS surfaces directory mutations (move-in,
  atomic-replace, separate `chmod`); polling sidesteps that entirely for
  the cost of up-to-5-seconds latency, which is well within the spec's
  observable budgets.
- On Linux/Windows the source returns empty: the DNS server still binds
  and responds REFUSED to all queries. There is no functional regression
  on those platforms because no `/etc/resolver/` flow is wired up there.

**Alternatives considered**:

- **Push API**: extend the HTTP admin API with
  `POST /v1/dns/suffixes` and `DELETE /v1/dns/suffixes/{suffix}`. Have
  `de install` call those endpoints after writing the file. Rejected:
  introduces a two-phase configuration that can fall out of sync (file
  present, daemon never notified, or vice versa). The pull model has a
  single source of truth.
- **Derive suffixes from the route registry**: extract suffix labels
  from registered hostnames (e.g. the last two labels). Rejected: brittle
  (which labels count as the suffix?), and it conflates routes with
  authoritative DNS zones. A suffix exists because it was installed via
  `de install`, not because a route happens to share its tail.
- **Configure suffixes at daemon startup only**. Rejected: violates
  FR-008 directly.
- **`fsnotify`-based file watcher on `/etc/resolver/`**. Rejected:
  dependency cost and watcher edge cases without commensurate latency
  win for the use case.

---

## R5. End-to-end probe shape for the doctor check

**Decision**: `de doctor`'s DNS check performs **two** probes and reports
both, rolled up into one user-visible result:

1. **Endpoint liveness probe**: directly opens a UDP connection to the
   DNS endpoint (`127.0.0.1:15354`) and sends a synthetic `A?` query for
   a name inside a configured suffix (or `dev.test` if the source has
   not yet populated). Expects a response within 250 ms. Failure here
   reports: "DNS endpoint not responding on `127.0.0.1:15354/udp`
   (devedged not running, port in use by something else, or DNS server
   not started)". This satisfies FR-007 precisely: the message names
   the endpoint, not the configuration file.
2. **System-resolver round-trip probe**: uses `net.LookupHost` on a
   synthetic name (`devedge-healthcheck.<suffix>`) for each currently
   configured suffix. Expects the result to contain `pkg/types.EdgeIP`.
   This validates the full path: system resolver framework →
   `/etc/resolver/<suffix>` → DNS endpoint → wildcard answer. Failure
   here with the endpoint probe passing indicates a misconfigured
   `/etc/resolver/` file (wrong port, wrong nameserver, missing).

Both probes have timeouts of 250 ms each. Total worst-case latency for
the DNS check is ~500 ms, well within the cost budget for a doctor run.

**Rationale**:

- The spec's FR-006 requires "an end-to-end resolution probe that
  exercises the per-domain resolver path" and FR-007 requires the failure
  message to identify the DNS endpoint specifically. Two probes cleanly
  separate "endpoint down" from "resolver-framework misconfig" so the
  message is always pointed at the actual failure rather than a fuzzy
  catch-all.
- The existing single-probe check (`net.LookupHost("devedge-healthcheck.dev.test")`)
  conflates these failure modes and emits "expected if hosts not
  configured yet" — exactly the misleading message the spec calls out.
- Both probes can be implemented with stdlib (`net` package) plus a
  small `miekg/dns` client; no new test dependencies.

**Alternatives considered**:

- A single `dig`-style external-binary call. Rejected: not all dev
  machines have `dig` available; spawning subprocesses for a doctor
  check is slower and harder to test.
- Only the system-resolver probe. Rejected: cannot distinguish
  "endpoint down" from "drop-in file wrong" without the lower-level
  probe.

---

## R6. Test layout and how to express the end-to-end requirement (FR-012)

**Decision**: Three test tiers, mirroring the project's testing pyramid:

1. **Unit** (`internal/dnsserver/*_test.go`): exercise the DNS handler
   directly via `miekg/dns`'s in-process `ResponseWriter` testing
   primitive. Verify A/AAAA/other-type behavior for in-suffix and
   out-of-suffix names, REFUSED semantics, and that suffix-set diffs are
   applied atomically.
2. **Integration** (`test/integration/dnsserver_test.go`): start the
   real daemon (`internal/daemon.Server.Run`) with a tempdir
   `DEVEDGE_HOME`, an injectable suffix source seeded with a synthetic
   suffix (no `/etc/resolver/` write needed), and an ephemeral DNS port.
   Send DNS queries from a `miekg/dns` client over UDP and TCP. Assert:
   answers match wildcard semantics, both transports return equivalent
   responses, and concurrent UDP+TCP queries succeed.
3. **End-to-end** (`test/e2e/resolver_macos_test.go`): macOS-only,
   build-tagged `//go:build darwin && e2e`. Writes `/etc/resolver/<test-suffix>`
   pointing at a daemon-bound DNS endpoint, starts the daemon, and uses
   the OS resolver via `net.LookupHost` to verify resolution. Requires
   root (because `/etc/resolver/` is owned by root). Skipped unless the
   environment variable `DEVEDGE_E2E_MACOS=1` is set and the user is
   root, so CI on non-macOS or unprivileged dev shells does not fail.

This satisfies FR-012 (end-to-end coverage of the macOS resolver path)
and Principle III (k3d-based e2e is not required for this feature; the
resolver framework is the boundary the spec calls out, and k3d does not
touch it).

**Rationale**:

- The bug being fixed is at the macOS resolver framework boundary; an
  e2e test that exercises *that* boundary directly is the highest-value
  regression guard. Wrapping it in k3d would test more than is necessary
  and obscure failure attribution.
- Build-tagging plus a privileged-execution gate prevents the e2e from
  destabilizing default `go test ./...` runs while still being trivially
  runnable on demand (`DEVEDGE_E2E_MACOS=1 sudo go test -tags=e2e ./test/e2e/...`).
- Integration tests cover the common path with no special privileges and
  catch most regressions in CI on any platform.

**Alternatives considered**:

- A docker-based macOS resolver simulation. Not feasible (macOS resolver
  framework is not available on Linux).
- Only integration tests, no e2e. Rejected: spec FR-012 explicitly
  requires an automated end-to-end test that exercises the macOS resolver
  framework.

---

## R7. Logging and observability fields (FR-009)

**Decision**: The DNS server emits structured `slog` events with these
field shapes:

- On startup: `event=dnsserver.started addr="127.0.0.1:15354"
  suffixes=[…]` (slice of strings).
- On suffix-set update: `event=dnsserver.suffixes_changed added=[…]
  removed=[…] now=[…]`.
- On query (debug-level by default; info-level if the query fell into a
  configured suffix but the handler returned an error): `event=dnsserver.query
  name=<…> qtype=<A|AAAA|…> transport=<udp|tcp> in_suffix=<bool> rcode=<…>`.
- On startup failure (port bind error, etc.): `event=dnsserver.bind_failed
  addr=<…> err=<…>` and the daemon surfaces the error in its top-level log
  but does not crash the rest of the daemon — HTTP API, proxy, reconciler
  continue to run, and `de doctor` reports the DNS endpoint as down.

**Rationale**:

- Satisfies FR-009's minimum: configured suffixes, endpoint startup, and
  resolution failures.
- Query-level logging at debug avoids log flood under normal use while
  still being available with one config flip when an operator is
  troubleshooting "why didn't `foo.dev.test` resolve?"
- Failing-open at startup (rest of daemon keeps running if the DNS bind
  fails) keeps the rest of the developer experience usable, and the
  doctor probe (R5) is the explicit, observable surface that says DNS
  is down. Failing closed (daemon refuses to start) would punish other
  unrelated functionality for a DNS-layer fault.

**Alternatives considered**:

- Per-query info-level logging. Rejected: chatty on cold caches and
  during dev-tool storms (`go build` triggers many GOPROXY DNS
  lookups), without commensurate signal.
- Daemon crashes on DNS bind failure. Rejected (above).

---

## Summary of decisions

| ID | Topic | Decision |
|----|-------|----------|
| R1 | DNS library | `github.com/miekg/dns` |
| R2 | Endpoint binding | Dedicated port `127.0.0.1:15354` (UDP+TCP); HTTP API stays on `:15353` |
| R3 | Answer semantics | `A` → `EdgeIP` (TTL 60s) for in-suffix names; NODATA for other types; REFUSED off-suffix |
| R4 | Suffix-set source | Poll `/etc/resolver/` every 5 s on macOS; empty set on other platforms |
| R5 | Doctor probe | Two probes (endpoint liveness + system-resolver round-trip), 250 ms each |
| R6 | Test layout | Unit (handler) + integration (daemon-bound) + e2e (macOS resolver framework, build-tagged) |
| R7 | Logging | Structured `slog` for startup, suffix changes, in-suffix query failures; debug-level for ordinary queries |

All Technical Context entries in `plan.md` are resolved. Phase 1 design may
proceed.
