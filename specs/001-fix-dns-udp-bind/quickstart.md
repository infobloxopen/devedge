# Quickstart: Verify DNS Resolution Through the macOS Resolver Framework

**Feature**: 001-fix-dns-udp-bind | **Phase**: 1
**Audience**: developers validating the implementation locally; reviewers
spot-checking that the bug is actually fixed.

This document is a hands-on confirmation that the fix delivers the
spec's P1 user story. Run it on a macOS workstation. It assumes the
implementation from `plan.md` has landed on the branch
`001-fix-dns-udp-bind`.

---

## Prerequisites

- macOS 14.x or 15.x (Apple silicon or Intel).
- A working Go 1.25.5 toolchain.
- `mkcert` already installed (the existing `de doctor` check verifies
  this).
- Root via `sudo` for `/etc/resolver/` writes and for the LaunchDaemon.

If you have an older `devedge` install on this machine, stop and remove
it before running this quickstart, to avoid stale `/etc/hosts` and
`/etc/resolver/` state interfering with the test:

```bash
sudo de stop
sudo rm -f /etc/resolver/dev.test
sudo killall mDNSResponder
```

---

## 1. Build and install from this branch

```bash
git checkout 001-fix-dns-udp-bind
make install      # builds and installs devedged + de
```

The `Configuring DNS...` step in the output now writes `/etc/resolver/dev.test`
with `port 15354` (was `15353` on `main`). Confirm:

```bash
cat /etc/resolver/dev.test
```

Expected output:

```
# Managed by devedge — do not edit
nameserver 127.0.0.1
port 15354
```

---

## 2. Start the daemon and verify both endpoints are listening

```bash
sudo de start
```

After a couple of seconds, check the daemon's bound sockets:

```bash
sudo lsof -nP -p "$(pgrep devedged)" | grep -E 'UDP|TCP' | grep -E '15353|15354'
```

Expected (exact PIDs and FDs will differ):

```
devedged  …  IPv4  …  0t0  TCP 127.0.0.1:15353 (LISTEN)
devedged  …  IPv4  …  0t0  UDP 127.0.0.1:15354
devedged  …  IPv4  …  0t0  TCP 127.0.0.1:15354 (LISTEN)
```

The two `15354` lines (UDP and TCP) are the new DNS endpoint. The
`15353/TCP` line is the unchanged HTTP admin API.

---

## 3. Direct DNS probes (bypassing the resolver framework)

These should succeed regardless of `/etc/resolver/` configuration.

UDP probe:

```bash
dig +short @127.0.0.1 -p 15354 anything.dev.test A
```

Expected output:

```
127.0.0.2
```

TCP probe:

```bash
dig +tcp +short @127.0.0.1 -p 15354 anything.dev.test A
```

Expected output:

```
127.0.0.2
```

REFUSED for off-suffix names:

```bash
dig +short @127.0.0.1 -p 15354 example.com A
```

Expected: empty output, and `dig` (without `+short`) reports
`status: REFUSED`.

NODATA for AAAA:

```bash
dig +short @127.0.0.1 -p 15354 anything.dev.test AAAA
```

Expected: empty output. With `+noshort` the `ANSWER SECTION` is empty and
`status: NOERROR`.

---

## 4. System-resolver-framework probe (the path that was broken)

This is the path the bug actually disabled. The resolver framework
(`/etc/resolver/<suffix>`) is consulted only by `getaddrinfo`-based
callers — browsers, `curl`, `ping`, Python's `socket`, and Go's cgo
resolver. `host` and `dig` deliberately bypass it and talk to the
servers in `/etc/resolv.conf`, so they will return NXDOMAIN for
`*.dev.test` even when the framework is working correctly.

Open a fresh shell so no process-local DNS cache is reused, then verify
with a getaddrinfo-based tool:

```bash
dscacheutil -q host -a name anything-fresh.dev.test
```

Expected output:

```
name: anything-fresh.dev.test
ip_address: 127.0.0.2
```

Or via Python's stdlib resolver:

```bash
python3 -c "import socket; print(socket.gethostbyname('anything-fresh.dev.test'))"
```

Expected output:

```
127.0.0.2
```

Or via Go's resolver (which mirrors how a browser would resolve):

```bash
go run ./cmd/de doctor
```

Expected (relevant lines):

```
  [PASS] DNS endpoint          UDP+TCP responsive on 127.0.0.1:15354
  [PASS] DNS *.dev.test        resolves to 127.0.0.2 via system resolver
  [PASS] macOS resolver        /etc/resolver/dev.test exists
```

The exact label text may differ from the example, but the three DNS-related
lines MUST all be PASS, and the resolution line MUST report `127.0.0.2`.

---

## 5. End-to-end through the browser

Register a project route, then open the hostname in a browser:

```bash
de register web.quickstart.dev.test http://127.0.0.1:3000
# Run any local web server on :3000 in another shell, e.g.
python3 -m http.server 3000
```

Open `https://web.quickstart.dev.test/` in a browser. You should see the
local web server's directory listing, served over devedge's HTTPS with
the mkcert-signed certificate.

Now try a hostname that has *never been registered* but is inside the
suffix:

```bash
de register web.quickstart.dev.test http://127.0.0.1:3000  # already registered
dscacheutil -q host -a name whatever-new.quickstart.dev.test
```

Expected:

```
name: whatever-new.quickstart.dev.test
ip_address: 127.0.0.2
```

This validates wildcard semantics (User Story 3): the unknown hostname
resolves to the local edge address even though no route exists for it
(HTTP-layer routing on the edge is a separate concern; DNS is the part
this feature changes).

---

## 6. Negative test: confirm the doctor catches DNS-down

With the daemon running, stop just the DNS endpoint by SIGSTOPing the
daemon's poll goroutine — easier in practice is to stop the daemon
entirely:

```bash
sudo de stop
de doctor
```

Expected:

```
  [FAIL] DNS endpoint          not responding on 127.0.0.1:15354/udp (devedged not running?)
  [FAIL] DNS *.dev.test        system resolver failed: no answer
  [PASS] macOS resolver        /etc/resolver/dev.test exists
```

The failure message MUST identify the DNS endpoint (FR-007). It MUST NOT
report "expected if hosts not configured yet."

Restart with `sudo de start` to recover.

---

## 7. Restart safety

```bash
sudo de stop
sudo de start
# wait two seconds
dscacheutil -q host -a name another-fresh.dev.test
```

Expected: `ip_address: 127.0.0.2` on the first attempt, without any
cache flush, without re-running `de install` (SC-007).

---

## 8. Cleanup

```bash
sudo de stop
sudo rm -f /etc/resolver/dev.test
sudo killall mDNSResponder
```

---

## What this quickstart verifies, mapped to the spec

| Quickstart step | Spec item |
|-----------------|-----------|
| §3 UDP probe | FR-001, FR-002 (UDP path) |
| §3 TCP probe | FR-002 (TCP path) |
| §3 REFUSED off-suffix | FR-004, SC-003 |
| §3 NODATA AAAA | FR-005 |
| §4 System resolver | FR-001 end-to-end, SC-001 |
| §5 Wildcard within suffix | FR-003, User Story 3, SC-002 |
| §6 Doctor failure message | FR-006, FR-007, SC-004 |
| §7 Restart safety | SC-007 |
