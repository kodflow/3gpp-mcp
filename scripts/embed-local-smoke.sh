#!/usr/bin/env bash
#
# embed-local-smoke.sh — prove the embed pipeline still works LOCALLY, with no
# Kaggle, no GPU, and (by default) no 2 GB model download. It exercises the same
# code the Kaggle kernel runs (seed → embed → validate), three ways:
#
#   1. Go tests for the embed path (deterministic local embedder + store scan).
#   2. A real CLI run of the embed binary on a seeded DB with EMBEDDER=local:
#      asserts null_at_floor==0 and that a --limit 1 recent-first run takes exactly
#      one (the newest) clause.
#   3. If data/models/bge-m3 is present, the same on the REAL BGE-M3 over ONNX on
#      CPU (ORT_EP=cpu) — the exact Kaggle path minus the CUDA EP — plus the
#      onnx-tagged byte-identity tests (pipeline / batch / graph-opt).
#
# Run:  make embed-smoke           (or)   scripts/embed-local-smoke.sh
set -euo pipefail
cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

GO="${GO:-go}"
command -v "$GO" >/dev/null || { echo "FATAL: go not on PATH"; exit 1; }
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
DB="$TMP/smoke.duckdb"
ORT_LIB="${ORT_LIB:-$PWD/data/models/onnxruntime/lib/libonnxruntime.so}"
BGE_DIR="${BGE_M3_DIR:-$PWD/data/models/bge-m3}"

pass() { printf '\033[0;32m[PASS]\033[0m %s\n' "$1"; }
info() { printf '\033[0;36m[ ... ]\033[0m %s\n' "$1"; }
die()  { printf '\033[0;31m[FAIL]\033[0m %s\n' "$1"; exit 1; }
# jget KEY FILE — pull a JSON scalar without a JSON dependency.
jget() { grep -oE "\"$1\"[[:space:]]*:[[:space:]]*[^,}]+" "$2" | head -1 | sed -E 's/.*:[[:space:]]*//; s/"//g'; }

# --- 1. Go tests (the authoritative, model-free proof) -----------------------
info "Go embed-path tests (local embedder + store scan)"
CGO_ENABLED=1 "$GO" test -count=1 ./internal/embed/ ./internal/store/ ./cmd/embed/ >/dev/null \
  || die "Go embed-path tests failed"
pass "Go embed-path tests"

# --- build the embed CLI + seeder --------------------------------------------
info "building cmd/embed + smoke seeder"
CGO_ENABLED=1 "$GO" build -o "$TMP/embed" ./cmd/embed       || die "build cmd/embed"
CGO_ENABLED=1 "$GO" build -o "$TMP/seed"  ./scripts/smoke-seed || die "build smoke-seed"

# cli_smoke BIN LABEL [pre-env...] — seed, full-embed (null_at_floor==0), then a
# recent-first --limit 1 (candidates==1). EMBEDDER/ORT_EP/etc. come from the caller.
cli_smoke() {
  local bin="$1" label="$2"
  "$TMP/seed" "$DB" >/dev/null || die "seed ($label)"
  "$bin" --db "$DB" --report json >"$TMP/full.json" 2>"$TMP/full.err" \
    || { cat "$TMP/full.err"; die "embed full ($label)"; }
  local nf emb_n model
  nf="$(jget null_embeddings_at_floor "$TMP/full.json")"
  emb_n="$(jget embedded_clauses "$TMP/full.json")"
  model="$(jget model "$TMP/full.json")"
  [ "${nf:-1}" = "0" ]    || die "$label: null_at_floor=$nf, want 0"
  [ "${emb_n:-0}" -ge 1 ] || die "$label: embedded=$emb_n, want >=1"
  pass "$label: full embed null_at_floor=0 embedded=$emb_n model=$model"

  "$TMP/seed" "$DB" >/dev/null
  "$bin" --db "$DB" --limit 1 --no-hnsw --report json >"$TMP/lim.json" 2>/dev/null \
    || die "embed --limit 1 ($label)"
  local cand
  cand="$(jget candidates "$TMP/lim.json")"
  [ "${cand:-0}" = "1" ] || die "$label: --limit 1 candidates=$cand, want 1"
  pass "$label: --limit 1 recent-first candidates=1"
}

# --- 2. local (hash) embedder CLI smoke --------------------------------------
EMBEDDER=local cli_smoke "$TMP/embed" "local-embedder"

# --- 3. real BGE-M3 over ONNX on CPU (only if the model is present) -----------
if [ -f "$BGE_DIR/model.onnx" ] && [ -f "$ORT_LIB" ]; then
  info "model present → building onnx CLI + running real BGE-M3 on CPU"
  CGO_ENABLED=1 "$GO" build -tags onnx -o "$TMP/embed-onnx" ./cmd/embed || die "build onnx cmd/embed"
  export ONNXRUNTIME_SHARED_LIBRARY_PATH="$ORT_LIB"
  export LD_LIBRARY_PATH="$(dirname "$ORT_LIB"):${LD_LIBRARY_PATH:-}"
  export BGE_M3_DIR="$BGE_DIR" ORT_EP=cpu
  unset EMBEDDER || true   # force the real onnx backend, never the hash embedder
  cli_smoke "$TMP/embed-onnx" "real-bge-m3-onnx-cpu"

  info "onnx byte-identity tests (pipeline / batch / graph-opt) on CPU"
  CGO_ENABLED=1 "$GO" test -tags onnx -count=1 ./internal/embed/ \
    -run 'TestBatchMatchesSingle|TestPipelineMatchesSerial|TestGraphOptMatchesDefault' >/dev/null \
    || die "onnx byte-identity tests"
  pass "onnx byte-identity tests"
else
  info "model absent (data/models/bge-m3) → skipping onnx/CPU layer (run 'make model' to include it)"
fi

printf '\n\033[0;32m✓ embed local smoke OK\033[0m\n'
