#!/usr/bin/env bash
# Resource-governed cargo wrapper.
#
# WHY: a full `cargo build`/`cargo test` of this workspace (duckdb-rs + ort +
# tokenizers) saturates every core and the linker is memory-heavy, which freezes
# the developer's host (mouse stalls, alt-tab dies). This wrapper:
#   - reserves ONE CPU core for the host UI (taskset pins cargo to cores 0..N-2),
#   - caps parallel build jobs at N-1,
#   - deprioritises the whole process tree (nice -n 19),
#   - hard-bounds wall-clock so a runaway build can never hang the box (timeout).
#
# Usage:  scripts/dev/rust-safe.sh <cargo-args...>
#   RUST_SAFE_TIMEOUT=1800 scripts/dev/rust-safe.sh test --manifest-path rust/Cargo.toml -p store-rs
set -euo pipefail

# Load the rustup/cargo env if cargo is not already on PATH.
if ! command -v cargo >/dev/null 2>&1; then
  # shellcheck disable=SC1090
  . "${CARGO_ENV:-$HOME/.cache/cargo/env}" 2>/dev/null || true
fi

CORES="$(nproc)"
JOBS="$(( CORES > 1 ? CORES - 1 : 1 ))"
LASTCPU="$(( CORES >= 2 ? CORES - 2 : 0 ))"   # reserve the top core for the UI
TIMEOUT="${RUST_SAFE_TIMEOUT:-1200}"          # 20 min default hard ceiling

export CARGO_BUILD_JOBS="$JOBS"
export CARGO_INCREMENTAL="${CARGO_INCREMENTAL:-0}"

echo "[rust-safe] cores=$CORES jobs=$JOBS pinned=0-$LASTCPU nice=19 timeout=${TIMEOUT}s :: cargo $*" >&2

exec timeout --signal=TERM --kill-after=30 "$TIMEOUT" \
  taskset -c "0-${LASTCPU}" \
  nice -n 19 \
  cargo "$@"
