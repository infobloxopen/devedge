#!/usr/bin/env bash
# lint-api-paths.sh — reject the version-after-resource URL anti-pattern in proto
# HTTP bindings.
#
# WHAT IT CATCHES
#   A REST path's version segment describes the contract used to interpret
#   everything after it, so it must come right after the domain/prefix and never
#   after a collection. This linter scans `google.api.http` path templates in
#   proto files and FAILS if a version segment
#
#       v[0-9]+                     e.g. v1, v2
#       v[0-9]+(alpha|beta)[0-9]+   e.g. v1beta1, v2alpha1
#
#   appears AFTER a literal resource segment in the same path:
#
#       BAD   /api/ipam/ip-spaces/v1/{id}   (version follows the "ip-spaces" collection)
#       GOOD  /api/ipam/v1/ip-spaces/{id}   (version precedes the collection)
#       GOOD  /v1/ip-spaces/{id}            (version-first, no domain prefix)
#
#   It prints each offending file:line and the bad path, then exits non-zero.
#
# HOW IT DECIDES
#   For every quoted path on an HTTP-verb line (get/put/post/patch/delete/custom,
#   including the `body`-carrying `custom` form), it walks the segments left to
#   right. A segment is "literal" when it is neither empty, a `{var}` / `{var=…}`
#   capture, nor a version token. The moment a literal segment has been seen, any
#   later version token is the anti-pattern. Segments before the first version
#   (the domain/group prefix such as `api` or `apis`, and the domain like `ipam`)
#   are allowed to precede the version — the check only fires once a *collection*
#   (a literal that itself is not immediately followed by the version) sits ahead
#   of a version. See the walk in check_path() for the exact rule.
#
# HEURISTIC LIMITS (kept deliberately cheap: grep/awk/bash, no buf plugin)
#   - It reads path STRINGS, not the proto AST. A path assembled at runtime or
#     spread across additional_bindings on separate lines outside a quoted
#     literal is not inspected.
#   - It treats any `vN`/`vNalphaM`/`vNbetaM` token as a version. A real resource
#     literally named "v2" (unusual, and against the naming rules) would trip it.
#   - It does not validate that a version is PRESENT — only that, when present, it
#     is not preceded by a collection. A path with no version segment passes.
#   - `{name=messages/*}`-style captures embed a literal ("messages") inside a
#     variable; the linter looks only at the top-level segment (the `{…}` capture)
#     and does not descend into the capture pattern.
#
# USAGE
#   hack/lint-api-paths.sh [DIR]        scan **/*.proto under DIR (default: repo root)
#   hack/lint-api-paths.sh --self-test  run the built-in good/bad fixtures
#
#   Wired as `make lint-api-paths`.
set -euo pipefail

# --- the core check, factored out so the self-test can reuse it -------------
#
# scan_file FILE -> prints "FILE:LINE\tPATH" for each offending path; returns 1
# if any offense was found in the file, 0 otherwise.
scan_file() {
  awk -v file="$1" '
    # Extract the quoted path from an HTTP rule line and test it.
    # Matches:  get: "…"   post:"…"   custom: { path: "…" }
    /(^|[[:space:]{])(get|put|post|patch|delete|path)[[:space:]]*:[[:space:]]*"/ {
      line = $0
      # pull the first double-quoted string on the line
      if (match(line, /"[^"]*"/)) {
        path = substr(line, RSTART + 1, RLENGTH - 2)
        if (path ~ /^\//) {                       # only absolute REST paths
          bad = check_path(path)
          if (bad) {
            printf "%s:%d\t%s\n", file, NR, path
            found = 1
          }
        }
      }
    }
    END { exit(found ? 1 : 0) }

    # check_path: 1 if a version segment follows a collection segment.
    #
    # The version segment must sit right after the domain/prefix, i.e. within the
    # allowed leading literals: an optional "api"/"apis" prefix, then an optional
    # single domain/group segment. Any further literal before the version is a
    # COLLECTION, so a version that follows it is the anti-pattern. Once the
    # version has appeared, any *second* version segment following a later literal
    # is likewise flagged.
    function check_path(p,    n, seg, i, s, isVer, isVar, leadLiterals, seenVersion, seenCollectionAfter) {
      n = split(p, seg, "/")
      leadLiterals = 0          # literal segments seen before the first version
      seenVersion = 0
      seenCollectionAfter = 0   # a literal seen after the first version
      for (i = 1; i <= n; i++) {
        s = seg[i]
        if (s == "") continue                     # leading slash / empty segments
        isVar = (s ~ /^\{.*\}$/)                   # {id}, {name=messages/*}
        isVer = (s ~ /^v[0-9]+((alpha|beta)[0-9]+)?$/)
        if (isVer) {
          if (!seenVersion) {
            # First version. It is fine only if at most the prefix + one domain
            # literal preceded it (api / apis + domain => leadLiterals <= 2).
            if (leadLiterals > 2) return 1
            seenVersion = 1
          } else {
            # A later version. Bad if a collection literal separated it from the
            # first version (e.g. /v1/things/v2 — version after a collection).
            if (seenCollectionAfter) return 1
          }
          continue
        }
        if (isVar) continue                        # a capture is not a collection literal
        # a literal, non-version segment.
        if (!seenVersion) {
          leadLiterals++
        } else {
          seenCollectionAfter = 1
        }
      }
      return 0
    }
  ' "$1"
}

# --- self-test: prove a good path passes and a bad path is flagged ----------
self_test() {
  local tmp status_good status_bad rc=0
  tmp="$(mktemp -d)"

  # Good: version precedes the collection (version-first). Must PASS.
  cat > "$tmp/good.proto" <<'EOF'
service IPAM {
  rpc GetIpSpace(GetReq) returns (IpSpace) {
    option (google.api.http) = { get: "/api/ipam/v1/ip-spaces/{id}" };
  }
}
EOF
  # Bad: version follows the "ip-spaces" collection. Must FAIL.
  cat > "$tmp/bad.proto" <<'EOF'
service IPAM {
  rpc GetIpSpace(GetReq) returns (IpSpace) {
    option (google.api.http) = { get: "/api/ipam/ip-spaces/v1/{id}" };
  }
}
EOF

  echo "self-test: good path (/api/ipam/v1/ip-spaces/{id}) should PASS"
  if scan_file "$tmp/good.proto"; then status_good=pass; else status_good=fail; fi
  echo "  -> $status_good"

  echo "self-test: bad path (/api/ipam/ip-spaces/v1/{id}) should be FLAGGED"
  if scan_file "$tmp/bad.proto"; then status_bad=notflagged; else status_bad=flagged; fi
  echo "  -> $status_bad"

  if [ "$status_good" = pass ] && [ "$status_bad" = flagged ]; then
    echo "self-test: OK"
  else
    echo "self-test: FAILED (good=$status_good bad=$status_bad)" >&2
    rc=1
  fi
  rm -rf "$tmp"
  return "$rc"
}

main() {
  if [ "${1:-}" = "--self-test" ]; then
    self_test
    return
  fi

  local dir="${1:-$(cd "$(dirname "$0")/.." && pwd)}"
  if [ ! -d "$dir" ]; then
    echo "lint-api-paths: not a directory: $dir" >&2
    return 2
  fi

  # Collect proto files, skipping vendored/generated trees that add noise.
  local files=()
  while IFS= read -r f; do
    files+=("$f")
  done < <(find "$dir" -type f -name '*.proto' \
             -not -path '*/vendor/*' \
             -not -path '*/testdata/*' \
             -not -path '*/.git/*' | sort)

  if [ "${#files[@]}" -eq 0 ]; then
    echo "lint-api-paths: no .proto files under $dir (nothing to check)"
    return 0
  fi

  local rc=0 offenders=""
  local f
  for f in "${files[@]}"; do
    local out
    if ! out="$(scan_file "$f")"; then
      offenders+="$out"$'\n'
      rc=1
    fi
  done

  if [ "$rc" -ne 0 ]; then
    echo "lint-api-paths: version-after-resource anti-pattern found" >&2
    echo "  (version must be the segment right after the domain/prefix, not after a collection)" >&2
    echo >&2
    printf '%s' "$offenders" | sed '/^$/d' | while IFS=$'\t' read -r loc path; do
      echo "  $loc" >&2
      echo "    bad path: $path" >&2
    done
    return 1
  fi

  echo "lint-api-paths: OK (${#files[@]} proto file(s) checked, no version-after-resource paths)"
}

main "$@"
