# Feature Specification: Daemon toolchain setup & doctor hardening

**Feature Branch**: `008-daemon-toolchain-install-doctor`
**Created**: 2026-06-10
**Status**: Draft

## Context

The 007 onboarding walk-through human run (2026-06-10, `specs/007/WALKTHROUGH.md`) produced six
friction findings before the developer could run `de project up`. Five remain as open defects:

- **#1** `requireDependencyTools()` fires only *after* the cluster is created (~40 s of work
  before the error surfaces).
- **#2** The `devedged` LaunchDaemon plist sets no PATH; the daemon runs with launchd's bare
  `/usr/bin:/bin:/usr/sbin:/sbin`, so every tool exec (helm, kubectl, k3d) fails silently until
  the developer manually adds PATH to the plist.
- **#3** `helm` and `kubectl` use `~/.kube/config` as their default kubeconfig, but the daemon
  runs as root and reads `/var/root/.kube/config`. The user's kube context (written by k3d
  CLI-side) is never seen by the daemon.
- **#4** Rancher Desktop's `kubectl` is a `kuberlr` shim that writes to `$HOME/.kuberlr`; when
  the daemon has no HOME it hits `mkdir .kuberlr: read-only file system` at `/`.
- **#5** `kuberlr` downloads ~57 MB on first use; devedge's port-forward establishment timeout
  (~20 s) kills the download. Workaround: pre-warm as root before `up`. Durable fix (client-go
  migration — Constitution IV) is deferred to a follow-on feature; **this feature adds the
  actionable error and pre-warm path via `de doctor`**.
- **#6** *(already patched in commit `577a649`)* relative `-f` produced a daemon-side ENOENT;
  regression test pinned; no remaining work.

The two systemic gaps named in the 007 test plan (`specs/007/WALKTHROUGH.md §Test Plan`):
1. e2es drive library seams in-process; never the built binary → daemon socket path.
2. Tests inherit the developer's rich shell env; never launchd's sanitized world.

This feature closes frictions #1–#5, adds the missing test harnesses (plist golden test,
launchd-equivalence harness, absolute-path invariant), and delivers a doctor-as-test check so
the toolchain is validated from the daemon's vantage before `up` can break.

## Clarifications

- **Scope of #5 fix**: client-go migration is out of scope. This feature ships: an actionable
  error when the port-forward kubectl exec fails (naming daemon-side PATH and HOME), and a
  `de doctor --toolchain` check that invokes each tool via the daemon API (so it validates from
  the daemon's vantage, including shim pre-warm).
- **Darwin-only for plist changes**: `Install()` is on `DarwinAdapter`. Linux (systemd unit) and
  unsupported adapters are unaffected by FR-002 for this release; the platform adapter interface
  is extended to express env discovery intent.
- **PATH discovery**: `de install` discovers the containing directories of `helm`, `kubectl`,
  `k3d`, and `mkcert` in the invoking user's PATH (the sudo-elevating shell), then writes a
  `/usr/local/bin`-prefixed union into `EnvironmentVariables.PATH` in the plist.
- **KUBECONFIG**: default to `$HOME/.kube/config` (user's home) in the plist; if `$KUBECONFIG`
  is set in the invoking shell, use that value instead.
- **HOME**: `os.UserHomeDir()` in the calling process (the `sudo` invoker); passed via the plist.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — `de install` sets up the daemon's full execution environment (P1) 🎯 MVP

A developer runs `sudo de install`. The written plist contains `EnvironmentVariables` entries for
`PATH` (includes dirs containing helm/kubectl/k3d/mkcert), `HOME` (the invoking user's home
dir), and `KUBECONFIG` (the invoking user's kubeconfig path). After `sudo launchctl load` the
daemon starts and `de project up` succeeds without the developer editing the plist by hand.

**Acceptance Scenarios**:

1. **Given** `helm`, `kubectl`, `k3d`, and `mkcert` are found in the invoking user's PATH,
   **When** `sudo de install` runs, **Then** the generated plist's `EnvironmentVariables` dict
   contains a `PATH` key whose value includes the directories of every discovered tool.
2. **Given** `$KUBECONFIG` is unset in the invoking shell, **When** `sudo de install` runs,
   **Then** the plist's `EnvironmentVariables` contains `KUBECONFIG` set to
   `<user-home>/.kube/config`.
3. **Given** `$KUBECONFIG` is set to a custom path, **When** `sudo de install` runs, **Then**
   the plist carries that custom path verbatim.
4. **Given** `$HOME` is the invoking user's home, **When** `sudo de install` runs, **Then** the
   plist carries `HOME` set to that home path.

**Independent Test**: golden test on `DarwinAdapter.Install()` output that asserts plist
contains PATH/KUBECONFIG/HOME keys (overrides HOME/KUBECONFIG env for determinism).

---

### User Story 2 — Early preflight: tool check fires before cluster operations (P1)

A developer with an incomplete PATH runs `de project up`. The tool-missing error fires immediately
(before any cluster creation), naming all missing tools in one message, so no cluster time is
wasted.

**Acceptance Scenarios**:

1. **Given** `helm` is absent from PATH, **When** `de project up` is run, **Then** the error
   fires before any cluster-create or helm call, and names all missing tools in one message.
2. **Given** all tools are present, **When** `de project up` runs normally, **Then** preflight
   passes and normal execution continues.

---

### User Story 3 — `de doctor` validates the daemon's toolchain (P1)

A developer running `de doctor` sees a "daemon toolchain" section that reports whether the daemon
can resolve helm/kubectl/k3d. If the daemon is running, the checks call the daemon API (so they
exercise the daemon's real PATH and HOME — not the shell's). If the daemon is not running, checks
are reported as skipped with a note.

**Acceptance Scenarios**:

1. **Given** the daemon is running and its environment includes helm/kubectl/k3d, **When** `de
   doctor` runs, **Then** it reports each tool as found, with the resolved absolute path.
2. **Given** the daemon is running but `kubectl` is missing from its PATH, **When** `de doctor`
   runs, **Then** it reports kubectl as not found and names the daemon-side PATH it searched.
3. **Given** the daemon is not running, **When** `de doctor` runs on toolchain checks, **Then**
   it reports them as skipped (daemon offline) rather than failing.

---

### User Story 4 — Launchd-equivalence harness: daemon tolderates sanitized env (P2)

An integration test starts `devedged` under `env -i PATH=/usr/bin:/bin DEVEDGE_HOME=<tmp>` and
issues a `de project up` (against a project that declares dependencies). The test asserts that the
resulting error is actionable (names the missing tools and the PATH it searched), and that no
cluster was mutated.

**Acceptance Scenarios**:

1. **Given** `devedged` is started with bare PATH (no helm/kubectl/k3d), **When** a project-up
   request arrives, **Then** the daemon returns an error that names the missing tools AND the
   PATH it searched.
2. **Given** `devedged` is started with bare PATH, **When** the same request arrives, **Then**
   no cluster-create or helm command is attempted (pre-mutation check).

---

### User Story 5 — Absolute-path invariant: every CLI→daemon path field is absolute (P2)

Any struct sent from the `de` CLI to `devedged` that contains a file path carries an absolute
path. Relative paths (from default flags like `-f devedge.yaml`) are resolved to absolute at the
CLI layer before the struct is marshalled into the daemon request.

**Acceptance Scenarios**:

1. **Given** `-f devedge.yaml` (relative), **When** the CLI builds the request struct, **Then**
   the path field is absolute (`filepath.Abs` resolved against the CLI's cwd).
2. **Given** `-f /tmp/devedge.yaml` (already absolute), **When** the CLI builds the request
   struct, **Then** the path is passed unchanged.

**Independent Test**: unit test that exercises the CLI path-building functions with both relative
and absolute inputs; asserts all produced struct fields are absolute.

## Failure Modes

| Failure | Observed behaviour |
|---------|-------------------|
| `helm`/`kubectl`/`k3d` not found at install time | `de install` prints a warning per missing tool, writes PATH with what was found, and continues (daemon won't work until tools are installed, but install should not block) |
| KUBECONFIG resolution fails (neither env nor default path exists) | warning printed; plist omits KUBECONFIG entry; on-screen message says to set `$KUBECONFIG` |
| Daemon not running when `de doctor --toolchain` runs | toolchain checks reported as "skipped (daemon offline)" |
| `kubectl` shim (kuberlr) times out on first download | actionable error names the exec, daemon-side PATH, and HOME; recommends pre-warm via `de doctor --toolchain` or running `de install` to include absolute tool path |

## Non-Goals

- client-go port-forward migration (deferred; separate feature, Constitution IV concern)
- Linux/Windows plist equivalents beyond what the platform adapter interface change already expresses
- Automatic daemon restart after `de install` (user responsibility; requires sudo)
- Full CLI-surface e2e (built binary + live daemon socket): deferred — the launchd-equivalence
  harness delivers the sanitized-env half; binary-launch e2e is a follow-on test gap

## Acceptance Criteria Checklist

- [ ] AC-001: `de install` plist includes PATH containing dirs of found tools.
- [ ] AC-002: `de install` plist includes HOME (invoking user's home dir).
- [ ] AC-003: `de install` plist includes KUBECONFIG ($KUBECONFIG or default ~/.kube/config).
- [ ] AC-004: `requireDependencyTools()` is called before any cluster or helm operation in `de project up`.
- [ ] AC-005: `de doctor` reports daemon toolchain (helm/kubectl/k3d) — resolved via daemon API when daemon is running, skipped when offline.
- [ ] AC-006: Daemon returns an actionable error naming tools + searched PATH when tools are missing.
- [ ] AC-007: Plist golden test passes (darwin adapter unit test).
- [ ] AC-008: Launchd-equivalence integration test passes (daemon under sanitized env returns actionable error, no cluster mutation).
- [ ] AC-009: Absolute-path invariant unit test passes (CLI path fields are absolute).
- [ ] AC-010: `make build` + `make lint` + unit + integration green.
