#!/usr/bin/env bash
# Generate the `de` CLI reference pages for the docs portal from the built
# binary's own --help output, so the reference cannot drift from the code.
#
# Usage: hack/gen-cli-docs.sh
# Output: docs/content/docs/reference/cli/{_index.md,<command>.md}
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
out_dir="$repo_root/docs/content/docs/reference/cli"
bin="$(mktemp -d)/de"

export NO_COLOR=1  # keep ANSI escapes out of the captured help text

echo "building de..."
go build -o "$bin" "$repo_root/cmd/de"

mkdir -p "$out_dir"

# subcommands_of PATH... -> prints the immediate subcommand names of `de PATH...`.
# Uses `de help PATH` (never `--help`) so passthrough commands such as
# `de ci run` (DisableFlagParsing) are rendered, not executed.
subcommands_of() {
  "$bin" help "$@" 2>/dev/null \
    | awk '/^Available Commands:/{f=1;next} /^[A-Za-z].*:$/{f=0} f && NF {print $1}' \
    | grep -vE '^(help|completion)$' || true
}

# emit_help HEADING_LEVEL PATH... -> writes a heading + fenced help block
emit_help() {
  local level="$1"; shift
  local hashes; hashes="$(printf '%.0s#' $(seq 1 "$level"))"
  {
    echo "${hashes} \`de $*\`"
    echo
    echo '```text'
    "$bin" help "$@" 2>/dev/null || true
    echo '```'
    echo
  } >> "$page"
}

# Recurse two levels: top-level command, then its subcommands.
gen_command_page() {
  local cmd="$1"
  page="$out_dir/$cmd.md"
  {
    echo "---"
    echo "title: de $cmd"
    echo "---"
    echo
    echo "> Generated from \`de $cmd --help\`. Run \`make docs-cli\` to refresh."
    echo
  } > "$page"
  emit_help 2 "$cmd"
  local sub
  for sub in $(subcommands_of "$cmd"); do
    emit_help 3 "$cmd" "$sub"
    local subsub
    for subsub in $(subcommands_of "$cmd" "$sub"); do
      emit_help 4 "$cmd" "$sub" "$subsub"
    done
  done
  echo "  generated reference/cli/$cmd.md"
}

# Section index.
cat > "$out_dir/_index.md" <<'EOF'
---
title: CLI reference
weight: 40
---

Every `de` command, generated from the binary's own help output. To refresh
after changing a command, run `make docs-cli`.
EOF

top_cmds="$(subcommands_of)"
echo "top-level commands: $(echo "$top_cmds" | tr '\n' ' ')"
for cmd in $top_cmds; do
  gen_command_page "$cmd"
done

echo "done -> $out_dir"
