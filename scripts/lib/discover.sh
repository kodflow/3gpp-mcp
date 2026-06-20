#!/usr/bin/env bash
# discover.sh — sourceable helpers to run the Rust rust/discover binary, the SOLE
# CI matrix/worklist discoverer (Phase 10 repoint complete; the Go cmd/discover
# shadow was retired in Phase 11b after byte-for-byte parity was proven — see
# rust/discover/src/lib.rs fixture tests and the frozen scripts/dev history).
#
# Usage:
#   source scripts/lib/discover.sh
#   discover_fetch_status "$URL" status.htm
#   discover_run status.htm ./rust/discover/target/release/discover raw  --emit-worklist --floor Rel-99 > worklist.txt
#   discover_run status.htm "$RB"                                   json --emit-draft-ledger --floor Rel-99 > drafts.json

# discover_fetch_status <url> <out_file> — GET the 3GPP status report with a
# browser User-Agent (3gpp.org 403s bots), retried.
discover_fetch_status() {
  local url="$1" out="$2"
  # shellcheck source=/dev/null
  source "$(git rev-parse --show-toplevel)/scripts/lib/retry.sh"
  retry curl -fsSL -A "Mozilla/5.0 (X11; Linux x86_64) discover" "$url" -o "$out"
}

# discover_run <status_file> <rust_bin> <compare:raw|json> <args…>
# Runs rust/discover on the status file with the given args and echoes its stdout.
# The third positional (raw|json) is retained for call-site compatibility with the
# former Go-shadow comparison; it no longer changes behaviour (Rust is the only
# producer). Exits non-zero if the Rust binary fails.
discover_run() {
  local sf="$1" rb="$2"
  shift 3 # drop status_file, rust_bin, and the legacy compare mode
  local r
  if ! r="$("$rb" --status-file "$sf" "$@" 2>/dev/null)"; then
    echo "discover_run: rust discover failed (args: $*)" >&2
    return 1
  fi
  printf '%s\n' "$r"
}
