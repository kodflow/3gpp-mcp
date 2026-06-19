#!/usr/bin/env bash
# discover.sh — sourceable helpers to run the Rust rust/discover binary in CI as the
# PRIMARY matrix/worklist tool while keeping Go cmd/discover as a live SHADOW that
# must agree (Phase 10 repoint, strangler-safe). A divergence on the real status
# report fails the step (fail-closed: halt, never silently mis-build). Phase 9 already
# proves byte-for-byte parity on a fixture; this extends that check to live data.
#
# Usage:
#   source scripts/lib/discover.sh
#   discover_fetch_status "$URL" status.htm
#   discover_run status.htm ./rust/discover/target/release/discover raw  --emit-worklist --floor Rel-99 > worklist.txt
#   discover_run status.htm "$RB"                                   json --emit-draft-ledger --floor Rel-99 > drafts.json

# discover_fetch_status <url> <out_file> — GET the 3GPP status report with the same
# browser User-Agent cmd/discover sends (3gpp.org 403s bots), retried.
discover_fetch_status() {
  local url="$1" out="$2"
  # shellcheck source=/dev/null
  source "$(git rev-parse --show-toplevel)/scripts/lib/retry.sh"
  retry curl -fsSL -A "Mozilla/5.0 (X11; Linux x86_64) discover" "$url" -o "$out"
}

_discover_norm_json() {
  python3 -c 'import sys,json; print(json.dumps(json.load(sys.stdin), sort_keys=True))'
}

# discover_run <status_file> <rust_bin> <compare:raw|json> <args…>
# Runs the Rust binary (primary) and Go cmd/discover (shadow) on the same status file
# with the same args; asserts identical output (raw byte compare, or JSON content
# compare for hand-formatted ledgers); echoes the Rust stdout. Exits non-zero on any
# divergence or either side failing.
discover_run() {
  local sf="$1" rb="$2" cmp="$3"
  shift 3
  local r g
  if ! r="$("$rb" --status-file "$sf" "$@" 2>/dev/null)"; then
    echo "discover_run: rust discover failed (args: $*)" >&2
    return 1
  fi
  if ! g="$(CGO_ENABLED=0 go run ./cmd/discover --status-file "$sf" "$@" 2>/dev/null)"; then
    echo "discover_run: go cmd/discover failed (args: $*)" >&2
    return 1
  fi
  if [ "$cmp" = json ]; then
    local rn gn
    rn="$(printf '%s' "$r" | _discover_norm_json)" || return 1
    gn="$(printf '%s' "$g" | _discover_norm_json)" || return 1
    if [ "$rn" != "$gn" ]; then
      echo "discover_run: JSON divergence Go vs Rust on live status (args: $*)" >&2
      echo "  go:   $gn" >&2
      echo "  rust: $rn" >&2
      return 1
    fi
  else
    if [ "$r" != "$g" ]; then
      echo "discover_run: divergence Go vs Rust on live status (args: $*)" >&2
      diff <(printf '%s\n' "$g") <(printf '%s\n' "$r") | head -30 >&2
      return 1
    fi
  fi
  printf '%s\n' "$r"
}
