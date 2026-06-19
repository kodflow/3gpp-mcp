#!/usr/bin/env bash
# discover-parity — end-to-end output parity between Go cmd/discover and the Rust
# rust/discover port (migration Phase 9 close-out). Runs BOTH CLIs against the same
# real status-report fixture + indexes via --status-file and diffs stdout per mode.
# A divergence means the Rust binary cannot yet replace the Go one in the CI matrix.
#
# The Rust build goes through rust-safe so running this locally reserves a CPU core
# and never freezes the host.
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

FIX="internal/catalog/testdata/status-report.htm"
[ -f "$FIX" ] || { echo "discover-parity: fixture missing: $FIX" >&2; exit 1; }

echo "discover-parity: building both binaries…" >&2
go build -o /tmp/discover-go ./cmd/discover
scripts/dev/rust-safe.sh build --manifest-path rust/discover/Cargo.toml >/dev/null
RB="rust/discover/target/debug/discover"

norm() { python3 -c 'import sys,json; print(json.dumps(json.load(sys.stdin),sort_keys=True))'; }

fail=0

# diff_raw NAME ARGS… — byte-compare stdout (matrix JSON array / worklist lines / TSV).
diff_raw() {
  local name="$1"; shift
  local g r
  g="$("/tmp/discover-go" --status-file "$FIX" "$@" 2>/dev/null)"
  r="$("$RB" --status-file "$FIX" "$@" 2>/dev/null)"
  if [ "$g" != "$r" ]; then
    echo "DIVERGE [$name]" >&2
    diff <(printf '%s\n' "$g") <(printf '%s\n' "$r") | head -20 >&2
    fail=1
  else
    echo "OK       [$name]" >&2
  fi
}

# diff_json NAME ARGS… — compare stdout after JSON normalisation (Go hand-formats
# the draft ledger, Rust uses serde_pretty; the CONTENT must match, not the bytes).
diff_json() {
  local name="$1"; shift
  local g r
  g="$("/tmp/discover-go" --status-file "$FIX" "$@" 2>/dev/null | norm)"
  r="$("$RB" --status-file "$FIX" "$@" 2>/dev/null | norm)"
  if [ "$g" != "$r" ]; then
    echo "DIVERGE [$name]" >&2
    echo "  go:   $g" >&2
    echo "  rust: $r" >&2
    fail=1
  else
    echo "OK       [$name]" >&2
  fi
}

# Empty index → full matrix (JSON array, both compact + sorted).
diff_raw  "delta (full matrix)"
diff_raw  "emit-worklist"        --emit-worklist
diff_raw  "list-drift"           --list-drift
diff_json "emit-draft-ledger"    --emit-draft-ledger

if [ "$fail" -ne 0 ]; then
  echo "discover-parity: FAIL — Go and Rust discover disagree" >&2
  exit 1
fi
echo "discover-parity: PASS — Go and Rust discover agree on every mode" >&2
