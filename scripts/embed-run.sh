#!/usr/bin/env bash
#
# embed-run.sh — PLATFORM-AGNOSTIC 3GPP embed runner.
#
# One script that bootstraps + runs cmd/embed on ANY Linux host: a Lightning
# Studio T4, a Kaggle kernel, a rented GPU box, or this devcontainer on CPU. The
# embed engine is the Go binary (cmd/embed); this script only provisions what it
# needs and invokes it with the throughput knobs + a tail-able log.
#
# It auto-detects a GPU (nvidia-smi) and selects ORT_EP=cuda accordingly; on CPU
# it uses the in-repo ORT. It is idempotent and resumable: existing toolchain,
# model, ORT lib and a per-series DB slice are reused, so re-runs are cheap.
#
# Env (all optional):
#   SERIES            2-digit 3GPP series to embed           (default 21)
#   EMBED_FLOOR       embed clauses at/above this release    (default Rel-99 = all)
#   LIMIT             cap the work-list (perf probe)         (default 0 = full series)
#   WORK              scratch dir                            (default /tmp/3gpp-embed)
#   REPO_DIR          repo checkout to build from            (default this repo)
#   BGE_BATCH         ONNX batch (else auto by VRAM)         (default: engine auto)
#   EMBED_SESSIONS_PER_GPU  overlap K sessions on one GPU    (default 1)
#   LATEST_DB_URL     lexical snapshot to slice from         (default GitHub latest)
#
# Usage:  SERIES=21 LIMIT=2000 scripts/embed-run.sh
set -euo pipefail
echo "::error::RETIRED (Phase 11b): the Go embed path was removed; embed runs in Rust via .github/workflows/corpus-rust-embed-kaggle.yml (kernel-rust-embed.py). This driver is kept for history only." >&2; exit 1

SERIES="${SERIES:-21}"
EMBED_FLOOR="${EMBED_FLOOR:-Rel-99}"
LIMIT="${LIMIT:-0}"
WORK="${WORK:-/tmp/3gpp-embed}"
REPO_DIR="${REPO_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
LATEST_DB_URL="${LATEST_DB_URL:-https://github.com/kodflow/3gpp-mcp/releases/download/latest/3gpp.duckdb.zst}"
ORT_VERSION="${ORT_VERSION:-1.26.0}"
mkdir -p "$WORK"

log() { printf '\033[0;36m[embed-run]\033[0m %s\n' "$*" >&2; }

# --- 1. device -------------------------------------------------------------
if command -v nvidia-smi >/dev/null 2>&1 && nvidia-smi -L >/dev/null 2>&1; then
  export ORT_EP=cuda
  GPU_DESC="$(nvidia-smi -L | head -1)"
  log "GPU detected → ORT_EP=cuda ($GPU_DESC)"
else
  export ORT_EP=cpu
  log "no GPU → ORT_EP=cpu"
fi

# --- 2. Go toolchain -------------------------------------------------------
if ! command -v go >/dev/null 2>&1; then
  GOV="$(grep -E '^go ' "$REPO_DIR/go.mod" | awk '{print $2}')"
  log "installing Go ${GOV}"
  curl -fsSL "https://go.dev/dl/go${GOV}.linux-amd64.tar.gz" -o "$WORK/go.tgz"
  sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf "$WORK/go.tgz"
  export PATH="/usr/local/go/bin:$PATH"
fi
log "go $(go version | awk '{print $3}')"

# --- 3. ONNX Runtime + BGE-M3 model ---------------------------------------
# CPU: reuse the in-repo artifacts fetched by scripts/fetch-model.sh. GPU: pull
# the CUDA ORT build (the in-repo lib is the CPU one) next to the model dir.
MODEL_DIR="${EMBED_MODEL_DIR:-$REPO_DIR/data/models/bge-m3}"
if [ ! -f "$MODEL_DIR/model.onnx" ]; then
  log "fetching BGE-M3 + CPU ORT via fetch-model.sh"
  ( cd "$REPO_DIR" && scripts/fetch-model.sh )
fi
if [ "$ORT_EP" = "cuda" ]; then
  ORT_DIR="$WORK/onnxruntime-gpu-$ORT_VERSION"
  ORT_LIB="$ORT_DIR/lib/libonnxruntime.so"
  if [ ! -f "$ORT_LIB" ]; then
    log "fetching ONNX Runtime GPU $ORT_VERSION"
    pkg="onnxruntime-linux-x64-gpu-$ORT_VERSION"
    curl -fsSL "https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/${pkg}.tgz" -o "$WORK/ort.tgz"
    mkdir -p "$ORT_DIR" && tar -C "$ORT_DIR" --strip-components=1 -xzf "$WORK/ort.tgz"
  fi
else
  ORT_LIB="${ONNXRUNTIME_SHARED_LIBRARY_PATH:-$REPO_DIR/data/models/onnxruntime/lib/libonnxruntime.so}"
fi
export ONNXRUNTIME_SHARED_LIBRARY_PATH="$ORT_LIB"
export LD_LIBRARY_PATH="$(dirname "$ORT_LIB"):${LD_LIBRARY_PATH:-}"
export EMBED_MODEL_DIR="$MODEL_DIR"
log "ORT lib: $ORT_LIB"

# --- 4. per-series DB slice (resumable) -----------------------------------
DB="$WORK/3gpp-s${SERIES}.duckdb"
if [ ! -f "$DB" ]; then
  SRC="$WORK/3gpp.duckdb"
  if [ ! -f "$SRC" ]; then
    log "downloading lexical latest snapshot"
    curl -fSL "$LATEST_DB_URL" -o "$WORK/3gpp.duckdb.zst"
    zstd -d -f "$WORK/3gpp.duckdb.zst" -o "$SRC"
  fi
  log "slicing series $SERIES into $(basename "$DB")"
  cp "$SRC" "$DB"   # the embed binary filters by --series; a copy keeps the source pristine + the slice writable
fi

# --- 5. build the embed binary --------------------------------------------
BIN="$WORK/embed-onnx"
log "building cmd/embed (-tags onnx)"
( cd "$REPO_DIR" && CGO_ENABLED=1 go build -tags onnx -o "$BIN" ./cmd/embed )

# --- 6. embed --------------------------------------------------------------
LOG="$WORK/embed-s${SERIES}.log"
: > "$LOG"
log "embedding series=$SERIES floor=$EMBED_FLOOR limit=$LIMIT ep=$ORT_EP → log $LOG"
LIM_ARG=(); [ "$LIMIT" -gt 0 ] 2>/dev/null && LIM_ARG=(--limit "$LIMIT")
"$BIN" --db "$DB" --series "$SERIES" --embed-floor "$EMBED_FLOOR" "${LIM_ARG[@]}" \
  --order recent --resume --checkpoint-every 2000 --progress-every 256 \
  --no-hnsw --require-semantic --log-file "$LOG" --report json
log "done — see $LOG for the cl/s trace"
