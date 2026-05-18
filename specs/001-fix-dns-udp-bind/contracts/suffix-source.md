# Contract: SuffixSource — Internal Platform Adapter

**Feature**: 001-fix-dns-udp-bind | **Phase**: 1
**Audience**: implementers of platform adapters in `internal/dnsserver/`;
tests that need to drive the suffix-set deterministically without
touching the host filesystem.

This contract defines the boundary between the platform-agnostic DNS
core and the platform-specific knowledge of "what suffixes are
authoritatively configured." It is the only place where platform
specialization lives in this feature (Principle IV).

## Implemented test coverage

| Obligation | Test |
|------------|------|
| Darwin: lists regular files, skips dot/non-regular | `internal/dnsserver/source_darwin_test.go:14` `TestDarwinSource_ListsRegularFiles` |
| Darwin: missing dir → empty, no error | `internal/dnsserver/source_darwin_test.go:42` `TestDarwinSource_MissingDir_ReturnsEmptyNoError` |
| Darwin: unreadable dir → error | `internal/dnsserver/source_darwin_test.go:54` `TestDarwinSource_UnreadableDir_ReturnsError` |
| Static source: defensive copy | `internal/dnsserver/source_test.go:8` `TestStaticSuffixSource_ReturnsDefensiveCopy` |
| Static source: Set propagates | `internal/dnsserver/source_test.go:32` `TestStaticSuffixSource_SetUpdatesNextList` |
| Poll loop applies diffs | `internal/dnsserver/server_test.go:154` `TestPollLoop_AppliesDiffs` |
| Poll loop retains prior set on error | `internal/dnsserver/server_test.go:210` `TestPollLoop_RetainsPriorSetOnSourceError` |
| Poll loop stops on ctx cancel | `internal/dnsserver/server_test.go:233` `TestPollLoop_CancellationStopsLoop` |

---

## Interface

```go
// Package internal/dnsserver
//
// SuffixSource enumerates the set of DNS suffixes the daemon should be
// authoritative for. The platform-specific implementation is selected at
// compile time via build tags.
type SuffixSource interface {
    // List returns the currently configured suffixes. The returned slice
    // must be safe for the caller to retain (the source must not mutate
    // it after returning). Ordering is not significant.
    //
    // Errors returned here are considered transient. The DNS server will
    // log the error and keep its prior view of the suffix set until the
    // next successful call.
    List(ctx context.Context) ([]ConfiguredSuffix, error)

    // Name returns a short identifier used in log messages
    // (e.g. "darwin-etc-resolver", "noop", "static"). It is a debug aid;
    // callers MUST NOT rely on its value.
    Name() string
}
```

---

## Implementations

### `darwinSuffixSource` (build tag `darwin`)

- Source of truth: directory listing of `/etc/resolver/`.
- Each regular file in the directory contributes one
  `ConfiguredSuffix` whose `Name` equals the file's base name,
  canonicalized (lowercased, no trailing dot, trimmed of leading/trailing
  whitespace if any).
- Files whose names are not syntactically valid DNS names per the
  validation rules in `data-model.md` are silently skipped (logged at
  debug level). Rationale: third-party software occasionally drops files
  here (e.g. macOS itself, or other tools) and they should not crash the
  daemon.
- Hidden files (names starting with `.`) and directory entries that are
  not regular files (symlinks resolved to a non-file target, sockets,
  etc.) are skipped.
- If `/etc/resolver/` does not exist, `List` returns an empty slice and
  no error. This is the expected state on a fresh macOS install where
  `de install` has never been run.
- If `/etc/resolver/` exists but is unreadable (rare; permission errors),
  `List` returns the error. The DNS server logs and retains its prior
  set.

### `noopSuffixSource` (build tag `!darwin`)

- Always returns `([]ConfiguredSuffix{}, nil)`.
- `Name()` returns `"noop"`.
- The DNS endpoint is still bound on these platforms (so port collisions
  surface during startup, and tests that inject a different source work
  cross-platform), but with no configured suffixes the endpoint answers
  REFUSED to all queries.

### `staticSuffixSource` (test-only, no build tag)

- Constructed with an explicit list of suffix names. `List` returns a
  copy of that list. Used by integration tests to drive the suffix set
  deterministically. May be reconfigured via a `Set(suffixes []string)`
  method for tests that exercise the add/remove path (FR-008).

---

## Polling contract

The DNS server runs a polling loop that calls `Source.List` at a
**bounded interval of 5 seconds**. The contract for the polling side
is:

- On startup, the server calls `List` once **synchronously** before
  beginning to serve. This ensures the first incoming query sees a
  populated suffix set (or, on a fresh install, sees the same empty set
  it would see on the next tick). Startup blocks on this initial call,
  with a 2-second timeout; if `List` exceeds that, the daemon logs the
  warning and proceeds with an empty set.
- Subsequent calls happen on a `time.Ticker` with 5-second period. Each
  call is performed under a 2-second per-call timeout to keep the
  polling loop bounded under pathological filesystem latency.
- Each successful call results in a diff against the previous in-memory
  set. The DNS server's `AuthoritativeSet.Replace` is called with the
  new slice, and the diff is logged at info level with the
  `dnsserver.suffixes_changed` event shape from research R7.
- A failing call retains the prior set and is logged at warn level
  (`dnsserver.suffix_poll_failed`). The loop continues.
- The loop terminates on `ctx.Done()`. No goroutine leak; the polling
  goroutine returns within one tick (worst case) of cancellation.

---

## Test obligations

The following behaviors MUST be covered before merge:

| Test | What it asserts |
|------|-----------------|
| `dnsserver/source_darwin_test.TestDarwinSource_ListsRegularFiles` | Files in a tempdir are listed; non-regular and dot-prefixed entries are skipped. (Skipped on non-darwin via build tag.) |
| `dnsserver/source_darwin_test.TestDarwinSource_MissingDir_ReturnsEmptyNoError` | `/etc/resolver/`-equivalent missing → `([], nil)`. |
| `dnsserver/source_test.TestStaticSource_ReturnsCopy` | `staticSuffixSource` returns a defensive copy. |
| `dnsserver/server_test.TestPollLoop_AppliesDiffs` | Mutating a `staticSuffixSource` between ticks leads to `AuthoritativeSet.Replace` being invoked with the new contents. |
| `dnsserver/server_test.TestPollLoop_RetainsPriorSetOnError` | A source that returns an error on one tick does not clear the previously-known suffixes. |
| `dnsserver/server_test.TestPollLoop_CancellationStopsLoop` | `ctx.Done()` stops the polling goroutine. |

---

## What this contract does NOT cover

- The on-disk format of `/etc/resolver/<suffix>` files. That is owned by
  `internal/dns/resolver_darwin.go` and continues to be the only writer.
  The suffix source only reads the directory listing — it does not parse
  file content.
- How `de install` decides which suffix to install. That is unchanged in
  this feature; the CLI still calls `dns.InstallResolverConfig("dev.test")`
  with a hardcoded value, and a future feature can lift that to be
  per-project.
- Notification semantics across processes (e.g. between the `de` CLI and
  the daemon). The daemon picks up changes via polling; no IPC is
  required.
