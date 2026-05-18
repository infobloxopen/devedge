# Contract: DNS Protocol — Client-Visible Behavior

**Feature**: 001-fix-dns-udp-bind | **Phase**: 1
**Audience**: DNS clients (the macOS resolver framework, `dig`, `host`,
`getaddrinfo` callers); regression tests in `test/integration` and
`test/e2e`.

This contract defines what observers of the DNS endpoint see. It is the
client-facing surface; internal Go types are described in
`../data-model.md`. The endpoint serves the standard DNS wire protocol
(RFC 1035 / 6891), but only a narrow subset of its semantic surface is
supported. This contract pins down exactly that subset.

## Implemented test coverage

Each obligation in the "Test obligations" table below is satisfied by a
named test. Pointers (file:line) for quick navigation:

| Obligation | Test |
|------------|------|
| Rule 1 (A) | `internal/dnsserver/handler_test.go:68` `TestHandler_AInSuffix_ReturnsEdgeIP` |
| Rule 1 (AAAA) | `internal/dnsserver/handler_test.go:96` `TestHandler_AAAAInSuffix_EmptyAnswer` |
| Rule 1 (other types) | `internal/dnsserver/handler_test.go:117` `TestHandler_OtherTypeInSuffix_EmptyAnswer` |
| Rule 2 (REFUSED off-suffix) | `internal/dnsserver/handler_test.go:136` `TestHandler_OutOfSuffix_Refused` |
| Empty set → REFUSED | `internal/dnsserver/handler_test.go:151` `TestHandler_EmptyAuthoritativeSet_AllRefused` |
| Rule 6 + trailing-dot | `internal/dnsserver/handler_test.go:163` `TestHandler_TrailingDotAndCase` |
| Rule 3 (FORMERR) | `internal/dnsserver/handler_test.go:179` `TestHandler_MalformedQuery_FormErr` |
| US3 — wildcard for unregistered name | `internal/dnsserver/handler_test.go:208` `TestHandler_NeverRegisteredName_ResolvesToEdgeIP` |
| US3 — deep subdomain | `internal/dnsserver/handler_test.go:225` `TestHandler_DeepSubdomain_Resolves` |
| US3 — handler ignores registry | `internal/dnsserver/handler_test.go:241` `TestHandler_HandlerDoesNotConsultRegistry` |
| UDP round-trip | `test/integration/dnsserver_test.go:146` `TestDNSServer_UDP_RoundTrip` |
| TCP round-trip | `test/integration/dnsserver_test.go:161` `TestDNSServer_TCP_RoundTrip` |
| Rule 5 — concurrent UDP/TCP | `test/integration/dnsserver_test.go:176` `TestDNSServer_UDP_TCP_Concurrent` |
| US3 wildcard via daemon | `test/integration/dnsserver_test.go:209` `TestDNSServer_WildcardForUnregisteredName` |
| FR-008 — add/remove without restart | `test/integration/dnsserver_test.go:225` `TestSuffixSet_AddRemovePropagatesWithoutRestart` |
| FR-012 — macOS resolver e2e | `test/e2e/resolver_macos_test.go:29` `TestResolverFrameworkPath` |

---

## Endpoint

- **Address**: `127.0.0.1:15354` (default). Configurable for tests via
  the daemon's `WithDNSAddr` option, but always bound on a loopback
  address.
- **Transports**: Both UDP and TCP. Both transports MUST be served and
  MUST return equivalent answers for the same query.
- **Visibility**: Loopback only. Not reachable from any non-loopback
  interface. Not advertised in `mDNS`, `LLMNR`, or any service-discovery
  channel.

---

## Configuration prerequisites

The endpoint answers based on the set of currently configured suffixes
(see `data-model.md` / `suffix-source.md`). If no suffix is configured,
the endpoint is still bound and accepting connections, but every query
receives `REFUSED` (RCODE 5).

---

## Query handling rules

Let `qname` be the canonicalized (lowercased, trailing-dot-stripped)
query name, and `qtype` be the query type.

### Rule 1 — Query name inside an authoritative suffix

If `qname` equals or is a subdomain of any configured suffix:

| `qtype` | RCODE | Answer section | Authority section |
|---------|-------|----------------|-------------------|
| `A` (IPv4) | `NOERROR` (0) | One `A` RR for `qname`, RDATA = `EdgeIP` (`127.0.0.2`), TTL = 60 s, class IN | empty |
| `AAAA` (IPv6) | `NOERROR` (0) | empty | empty |
| any other type (`MX`, `TXT`, `SRV`, `CNAME`, `PTR`, …) | `NOERROR` (0) | empty | empty |

Notes:

- For `A` queries, the answer is **synthetic and wildcard**: the same RR
  is returned for `dev.test`, `foo.dev.test`, `bar.foo.dev.test`, and any
  name in the subtree.
- No `SOA` is added to the authority section on empty answers. This is
  intentional (see research R3): we are not a conventional zone-style
  authority, and emitting an SOA would imply negative-cache semantics
  that this endpoint does not provide.
- TTL of 60 seconds is fixed for `A` responses.

### Rule 2 — Query name not inside any authoritative suffix

If `qname` does not match any configured suffix:

- RCODE = `REFUSED` (5).
- Answer, authority, and additional sections are empty.
- The query is logged at debug level for diagnosis.

### Rule 3 — Malformed query

If the incoming message is not parseable as a valid DNS request, or has
zero questions, or has more than one question (illegal by RFC 1035 even
though some implementations accept it):

- RCODE = `FORMERR` (1) if the parser can reach the header.
- For unparseable framing on TCP, the connection is closed.
- For unparseable UDP datagrams, the datagram is dropped silently.

### Rule 4 — UDP message size and truncation

If a UDP response would exceed the size negotiated via EDNS(0) (or 512
bytes if the client did not advertise EDNS), the response is set with
the truncation (`TC`) bit and the client retries over TCP. With the
current single-A-record answer shape and no SOA, responses fit easily in
512 bytes for any realistic query name length, so truncation is not
expected under normal use. The contract still requires that the
truncation path is correct if it ever activates, because macOS's
resolver framework will fall back to TCP on truncation.

### Rule 5 — Concurrent queries

The endpoint MUST handle concurrent queries arriving over UDP and TCP
without response cross-talk or deadlock. Specifically, no observable
serialization between transports should be visible to clients.

### Rule 6 — Case insensitivity

DNS name comparison is case-insensitive (RFC 1035 §2.3.3). A query for
`FOO.DEV.TEST` against a configured suffix `dev.test` MUST be answered
under Rule 1. The case of the original `qname` is preserved in the
answer section's owner-name field (0x20-encoding compatible).

---

## What clients can rely on

- `getaddrinfo`-style IPv4 resolution for any name inside a configured
  suffix returns `127.0.0.2`.
- `getaddrinfo`-style IPv6 resolution for the same name returns no
  addresses (NODATA), allowing fallback to the IPv4 path.
- `dig +short @127.0.0.1 -p 15354 foo.<suffix> A` returns `127.0.0.2`.
- `dig +tcp +short @127.0.0.1 -p 15354 foo.<suffix> A` returns
  `127.0.0.2`.
- `dig @127.0.0.1 -p 15354 example.com A` returns `REFUSED`.
- Removing the `/etc/resolver/<suffix>` file and waiting for the next
  poll tick causes subsequent queries for that suffix to return
  `REFUSED`.

---

## What clients must NOT rely on

- Per-name distinct answers (every name in the suffix resolves to the
  same address).
- Authoritative negative caching via `SOA` minimum TTL (no SOA returned).
- Stable order of multiple `A` records (there is at most one).
- Resolution of names outside any configured suffix (REFUSED).
- IPv6 addresses for in-suffix names (NODATA today; may become non-empty
  in a future feature).
- A particular TCP keepalive or pipelining behavior beyond what
  `miekg/dns`'s default server provides.

---

## Test obligations

The following observable behaviors MUST be covered by automated tests
before this feature is merged (mapped to spec FR / SC):

| Test | What it asserts | Source spec item |
|------|-----------------|------------------|
| `dnsserver_test.TestHandler_AInSuffix_ReturnsEdgeIP` | Rule 1 (A) returns `EdgeIP` with TTL 60. | FR-001, FR-003 |
| `dnsserver_test.TestHandler_AAAAInSuffix_EmptyAnswer` | Rule 1 (AAAA) returns NOERROR + empty answer. | FR-005 |
| `dnsserver_test.TestHandler_OtherTypeInSuffix_EmptyAnswer` | Rule 1 (other types) returns NOERROR + empty. | FR-005, Edge Case "responses for unsupported record types" |
| `dnsserver_test.TestHandler_OutOfSuffix_Refused` | Rule 2 returns REFUSED. | FR-004, SC-003 |
| `dnsserver_test.TestHandler_TrailingDotAndCase` | Rule 6 + trailing-dot normalization. | Edge Case "case-insensitive lookup" |
| `integration/dnsserver_test.TestDNSServer_UDP_RoundTrip` | A real `miekg/dns` client over UDP gets the expected wildcard answer. | FR-002, SC-001, SC-005 |
| `integration/dnsserver_test.TestDNSServer_TCP_RoundTrip` | Same over TCP. | FR-002 |
| `integration/dnsserver_test.TestDNSServer_UDP_TCP_Concurrent` | Concurrent UDP/TCP load returns valid responses. | Edge Case "concurrent queries", SC-005 |
| `integration/dnsserver_test.TestSuffixSet_AddRemovePropagatesWithoutRestart` | After mutating the suffix source, queries for the new/removed suffix start/stop being answered within one poll cycle, without daemon restart. | FR-008 |
| `e2e/resolver_macos_test.TestResolverFrameworkPath` (build-tagged) | After `de install`-equivalent setup, `net.LookupHost("…<suffix>")` returns `EdgeIP`. | FR-012, SC-001, SC-002 |
