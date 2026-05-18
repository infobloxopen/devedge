# Feature Specification: DNS Resolution Through macOS Per-Domain Resolver Framework

**Feature Branch**: `001-fix-dns-udp-bind`
**Created**: 2026-05-17
**Status**: Draft
**Input**: User description: "issue #6 — devedged DNS server binds TCP-only on :15353; /etc/resolver/* unusable on macOS"
**Tracking Issue**: [#6](https://github.com/infobloxopen/devedge/issues/6)

## Background

`de install` configures a macOS per-domain resolver drop-in (`/etc/resolver/<domain>`)
that instructs the system to route DNS queries for the configured domain suffix to a
local devedge endpoint. macOS sends those queries primarily over UDP. Today no
endpoint answers DNS over UDP at the address the drop-in points at, so every
hostname lookup for the configured suffix silently fails (`NXDOMAIN` / timeout),
even though `de install` reports success, `de doctor` reports green, and `/etc/hosts`
contains the expected mapping. The hosts file is bypassed because macOS routes any
query matching a configured per-domain resolver suffix exclusively through that
resolver path.

The result is that the documented developer flow — `de install`, then open a
project hostname in the browser — does not work, and the failure mode is
indistinguishable from a routing or certificate problem from the user's
perspective.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Project hostnames resolve immediately after `de install` (Priority: P1)

A developer on macOS runs `de install` for a project whose hostnames share a
configured suffix. From the moment installation reports success, every hostname
inside that suffix that is intended to reach the local edge MUST resolve to the
local edge address — through both `getaddrinfo`-based clients (browsers, `curl`)
and direct DNS lookups against the per-domain resolver path.

**Why this priority**: This is the contract `de install` advertises and the
primary developer experience for the platform. Without it, no other devedge
feature is reachable through the configured hostnames, so this is the
single highest-impact behavior to restore.

**Independent Test**: After `de install` completes and a project hostname has
been registered, opening the hostname in a browser, running `curl https://<host>`,
and running a DNS-level probe (`dig`, `host`, `dscacheutil -q host`) against the
hostname all succeed and return the local edge address. The check holds whether
the hostname is queried over UDP or TCP at the DNS layer, since macOS may use
either.

**Acceptance Scenarios**:

1. **Given** `de install` has just completed for a configured suffix, **When**
   the developer queries any registered hostname inside that suffix using
   `host`, `dig`, `dscacheutil -q host`, or a browser, **Then** the lookup
   succeeds and returns the configured local edge address with no manual
   intervention (no DNS cache flush, no resolver restart, no editing of
   `/etc/hosts`).
2. **Given** the system is configured and serving, **When** a DNS client sends
   the same query first over UDP and then over TCP for the configured DNS
   endpoint, **Then** both transports return the same authoritative answer.
3. **Given** the system is configured and serving, **When** the developer
   registers a new hostname inside an already-configured suffix, **Then** the
   new hostname becomes resolvable through the per-domain resolver path
   without re-running `de install` and without restarting the resolver
   subsystem.

---

### User Story 2 — `de doctor` accurately reports DNS resolution health (Priority: P2)

When the per-domain resolver path is not actually delivering working DNS
resolution, `de doctor` MUST surface that failure with a specific, actionable
message. Reporting "green" while DNS is broken is itself a defect, because it
sends developers chasing routing or certificate causes for a DNS-layer fault.

**Why this priority**: The diagnostic must match reality. Without this, fixing
the underlying bug is only half the value — a future regression in the DNS
path would once again be hidden behind a misleading "all green" report. This
follows directly from the Safe Reconciliation and Observable Operations
principle.

**Independent Test**: With the DNS endpoint intentionally not serving (e.g.
listener stopped, port hijacked), `de doctor` MUST report a failure for the
DNS check and the message MUST identify that DNS resolution through the
per-domain resolver path is the failing concern. With the DNS endpoint
healthy, `de doctor` MUST report success and that report MUST be backed by an
actual query that exercises the resolver path, not only by the presence of
configuration files.

**Acceptance Scenarios**:

1. **Given** the per-domain resolver drop-in is installed but no listener is
   answering queries on the configured DNS endpoint, **When** the developer
   runs `de doctor`, **Then** the DNS-related check reports a failure and the
   message points to the DNS endpoint not responding (not to "expected if
   hosts not configured yet" or any other unrelated cause).
2. **Given** the per-domain resolver drop-in is installed and the DNS
   endpoint is responding correctly, **When** the developer runs `de doctor`,
   **Then** the DNS-related check reports success and the report reflects an
   actual successful end-to-end resolution probe, not only that the
   configuration file exists on disk.

---

### User Story 3 — Wildcard hostnames within a configured suffix resolve to the local edge (Priority: P3)

For any hostname inside a configured suffix — including hostnames that have
not been individually registered — DNS resolution MUST return the local edge
address. This is the property that the per-domain resolver path is supposed
to deliver and the reason it exists in addition to the `/etc/hosts`
mechanism: per-host entries cannot cover arbitrary subdomains.

**Why this priority**: This is the differentiated capability of the resolver
path. Without it, the `/etc/hosts` fallback would be sufficient for fully
known hostnames, and the resolver drop-in adds no value over hosts entries.
With it, developers can use new subdomains under a configured suffix without
re-registering. This is lower priority than P1 only because exact-match
resolution for already-registered hostnames is the more common path; once
wildcard works it should remain working.

**Independent Test**: Query an arbitrary hostname inside a configured suffix
that has never been individually registered (for example,
`anything-new.<suffix>`) using `host` or `dig` against the DNS endpoint and
through the system resolver. The response MUST be the local edge address.
Querying a hostname *outside* any configured suffix MUST NOT be answered by
the devedge DNS endpoint (it falls through to the system's normal resolution
path).

**Acceptance Scenarios**:

1. **Given** a suffix has been configured via `de install`, **When** the
   developer queries a hostname inside that suffix that has never been
   individually registered, **Then** the response is the local edge address
   and the lookup succeeds for both UDP and TCP transports.
2. **Given** any suffix has been configured, **When** the developer queries a
   hostname that is not inside any configured suffix, **Then** the devedge
   DNS endpoint does not return an authoritative answer for that name and
   the system resolver continues normal upstream resolution for it.

---

### Edge Cases

- The DNS endpoint MUST tolerate concurrent queries arriving over both UDP
  and TCP without deadlock or response corruption.
- macOS may issue queries for record types beyond `A` (notably `AAAA`,
  `PTR`, and various service-discovery records). For names inside a
  configured suffix that the system is responsible for, responses for
  unsupported record types MUST be well-formed empty answers rather than
  malformed responses or silent drops, so clients can fall through cleanly
  to other record types.
- macOS aggressively caches negative DNS responses. Behavior MUST not depend
  on the developer manually flushing the system DNS cache or restarting
  `mDNSResponder` after a successful `de install`. If a cache flush is
  unavoidable for the very first resolution after install, the install path
  MUST handle it transparently.
- Multiple suffixes may be configured at once (e.g. `dev.test`,
  `dk-local.test`). Queries for any configured suffix MUST be answered;
  queries for any suffix that has been deconfigured MUST stop being answered
  promptly after deconfiguration so stale entries do not survive.
- An incoming query for a name that exactly matches a hostname stored in
  the managed `/etc/hosts` section MUST resolve to the same address whether
  the resolver path answers it or the hosts path answers it. The two paths
  MUST not disagree.
- DNS messages exceeding the UDP-safe payload size MUST be handled
  correctly (either through proper truncation signalling so the client
  retries over TCP, or through EDNS), so larger answers do not silently
  fail.
- The DNS endpoint MUST start serving on daemon startup before
  `de install`-style flows depend on it, and MUST stop cleanly on daemon
  shutdown without leaving an unresponsive port behind.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The system MUST answer DNS queries for any hostname inside a
  configured suffix when those queries arrive at the local DNS endpoint
  referenced by the macOS per-domain resolver drop-in. "Answer" means
  returning a well-formed DNS response with the local edge address for the
  query types the system is responsible for.
- **FR-002**: The system MUST answer those queries over both UDP and TCP
  transports, because macOS may use either depending on response size,
  truncation, and platform policy. Behavior MUST be equivalent across the
  two transports for the same query.
- **FR-003**: The system MUST answer queries for hostnames that have never
  been individually registered, as long as those hostnames are inside a
  configured suffix (wildcard semantics for the suffix). This is the
  property that motivates having a resolver path in addition to
  `/etc/hosts`.
- **FR-004**: The system MUST NOT answer authoritatively for any name
  outside a configured suffix; such queries MUST be left to the system's
  normal upstream resolution path so devedge does not become a black hole
  for unrelated traffic.
- **FR-005**: The system MUST return responses for record types it does not
  affirmatively support as well-formed empty answers, not as errors or
  silent drops, so that clients querying record types beyond `A` continue
  to function normally.
- **FR-006**: `de doctor`'s DNS-related check MUST verify behavior by
  performing an end-to-end resolution probe that exercises the per-domain
  resolver path, not only by checking that configuration files exist on
  disk. A passing report MUST require that an actual query succeeded.
- **FR-007**: `de doctor`'s DNS-related check MUST report failure with a
  specific, actionable message when the per-domain resolver path is not
  delivering working resolution, and the message MUST identify "DNS
  endpoint not responding" or an equivalent root cause rather than
  defaulting to ambiguous "expected if hosts not configured yet"
  messaging.
- **FR-008**: The system MUST keep the per-domain resolver configuration
  and the actively-served suffixes in agreement: if a suffix is configured
  via `de install`, queries for it MUST be answered; if a suffix is
  removed, queries for it MUST stop being answered without requiring a
  daemon restart.
- **FR-009**: The system MUST emit structured logs for DNS query handling
  sufficient for an operator to determine, after the fact, why a given
  hostname did or did not resolve. At minimum: configured suffixes,
  startup of the DNS endpoint, and resolution failures for queries inside
  a configured suffix.
- **FR-010**: The behavior MUST persist across daemon restarts: after the
  daemon is stopped and started again, resolution MUST work without any
  additional user action.
- **FR-011**: Existing managed `/etc/hosts` behavior MUST continue to work
  for hostnames that do not fall under a configured per-domain resolver
  suffix. Removing managed hosts entries is out of scope for this feature.
- **FR-012**: The fix MUST be testable by an automated end-to-end test
  that exercises the macOS resolver framework path under realistic
  conditions, in line with the project's end-to-end testing principle.
  The test MUST fail today (demonstrating the bug) and pass after the
  fix, and MUST cover at least the P1 user story.

### Key Entities

- **Configured suffix**: A DNS suffix (e.g. `dev.test`) that the system
  has registered with the macOS per-domain resolver framework and for
  which it is responsible for answering DNS queries.
- **Local edge address**: The loopback alias on which devedge serves
  HTTP/HTTPS for project hostnames. DNS answers for configured suffixes
  resolve to this address. The specific value is determined by existing
  system behavior and is not changed by this feature.
- **DNS endpoint**: The local network endpoint to which the macOS
  per-domain resolver drop-in directs queries for configured suffixes.
  Its concrete address and port are implementation choices made during
  planning; the spec requires only that the endpoint exists, answers
  correctly over both UDP and TCP, and matches whatever address the
  drop-in file references.

## Assumptions

- The fix is scoped to macOS, where the per-domain resolver framework
  exists and where the reported failure occurs. Linux and Windows do not
  use `/etc/resolver/` and are unaffected by this change; their DNS
  behavior is not in scope.
- The wildcard answer for any name inside a configured suffix is the local
  edge address. This matches the existing design comment in the resolver
  installer ("provides wildcard support for hostnames not yet explicitly
  registered") and the fact that all project traffic is funneled through
  the same local edge. A future feature may serve per-hostname distinct
  answers; that is out of scope here.
- The managed `/etc/hosts` mechanism remains in place as a complement, not
  a replacement, for the resolver path. Hosts entries cover names outside
  configured suffixes and provide a working baseline if the resolver path
  is intentionally disabled.
- IPv4 (`A` records) is the required response type. `AAAA` and other
  types are returned as well-formed empty answers per FR-005; full
  IPv6 support, if needed, is a separate feature.
- The DNS endpoint runs in-process with the existing daemon. Adding a
  separate daemon process is out of scope.
- The specific port number used for the DNS endpoint is an implementation
  decision and is allowed to change from today's value if needed to
  resolve TCP-vs-HTTP conflicts on the same port; the per-domain resolver
  drop-in is rewritten consistently as part of the implementation.

## Out of Scope

- DNS resolution on Linux or Windows.
- Per-hostname distinct DNS answers within a suffix (every name in the
  suffix resolves to the same local edge address).
- Recursive resolution for names outside any configured suffix.
- DNSSEC or any cryptographic validation of responses.
- Authoritative responses for record types other than `A` (with
  well-formed empty answers for unsupported types).
- Removing or redesigning the managed `/etc/hosts` mechanism.
- Exposing the DNS endpoint on any interface other than loopback.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On a clean macOS workstation, after `de install` completes
  for a project with at least one registered hostname inside a
  configured suffix, that hostname MUST resolve to the local edge
  address on the first attempt with no manual intervention (no
  `dscacheutil -flushcache`, no `sudo killall mDNSResponder`, no
  `/etc/hosts` edit). Measured as: a fresh `host <hostname>` invocation
  from a new shell returns the expected address.
- **SC-002**: For a configured suffix, an arbitrary hostname inside that
  suffix that was never individually registered MUST resolve to the
  local edge address on the first attempt. Measured as: `host
  arbitrary-name.<suffix>` returns the expected address.
- **SC-003**: A hostname that is not inside any configured suffix MUST
  NOT be diverted by the devedge resolver path. Measured as: `host
  example.com` returns its normal public answer, not a loopback
  answer, while a devedge suffix is configured.
- **SC-004**: When the DNS endpoint is intentionally taken down, `de
  doctor` reports a DNS failure with a message that identifies the DNS
  endpoint as the unresponsive component, within the same run, with no
  separate flag required. When the endpoint is healthy, `de doctor`
  reports DNS success only after a real resolution probe succeeds.
- **SC-005**: 100% of the time across at least 100 consecutive queries
  mixing UDP and TCP transports against the DNS endpoint, the endpoint
  returns a valid DNS response for queries inside a configured suffix
  (no timeouts, no malformed responses, no protocol mismatches).
- **SC-006**: End-to-end resolution latency for queries inside a
  configured suffix, measured at the system-resolver layer (`host` /
  `dscacheutil -q host`), is under 100 ms at the 95th percentile on a
  developer laptop with no contention.
- **SC-007**: After stopping and starting the daemon, the next
  resolution attempt for a configured-suffix hostname succeeds without
  any developer action and without an `/etc/hosts` flush.
- **SC-008**: At least one automated end-to-end test, exercising the
  macOS resolver framework path under realistic conditions, fails
  against the current behavior and passes after the fix. This is the
  regression guard required by the project's end-to-end testing
  principle.
