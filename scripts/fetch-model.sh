#!/usr/bin/env bash
#
# fetch-model.sh — bootstrap the REAL semantic backend (BGE-M3 via ONNX Runtime)
# used when the binary is built with `-tags onnx`. Default builds DON'T need
# this: they run lexical (BM25) or the deterministic local embedder
# (EMBEDDER=local). This downloads ~2.4 GB into data/models/ and is meant to be
# run by a human (or a release pipeline), once. It is idempotent and resumable.
#
# Usage:   scripts/fetch-model.sh                  # embedder only (BGE-M3 + ORT)
#          WITH_RERANKER=1 scripts/fetch-model.sh  # + cross-encoder (see A3)
#          WITH_SPARSE=1   scripts/fetch-model.sh  # + learned-lexical (sparse) head
# Then:    make build-onnx
#          make ingest-onnx ARGS="--spec 33.128"
#
# Env overrides:
#   ORT_VERSION   ONNX Runtime version  (default 1.26.0 — matches onnxruntime_go v1.30.x / API 25)
#   MODELS        target dir            (default <repo>/data/models)
#   WITH_RERANKER fetch the reranker    (default 0; source resolved in A3)
#   WITH_SPARSE   export/fetch the BGE-M3 sparse head (default 0; SPARSE_ONNX_URL optional)
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODELS="${MODELS:-$ROOT/data/models}"
mkdir -p "$MODELS"

# Resumable, fail-on-error download with a stable User-Agent (HF rejects some).
dl() { # dl <url> <dest>
  local url="$1" dest="$2"
  if [[ -f "$dest" ]]; then echo "  ✓ $(basename "$dest") (present)"; return; fi
  echo "  → $(basename "$dest")"
  curl -fSL --retry 3 --retry-delay 2 -C - -A "Mozilla/5.0" "$url" -o "$dest.part"
  mv "$dest.part" "$dest"
}

# sha256_verify <expected-hex> <file> — portable across Linux (sha256sum) and
# macOS (shasum -a 256). Exits non-zero on mismatch.
sha256_verify() {
  if command -v sha256sum >/dev/null 2>&1; then echo "$1  $2" | sha256sum -c -
  elif command -v shasum >/dev/null 2>&1; then echo "$1  $2" | shasum -a 256 -c -
  else echo "no sha256 tool (sha256sum/shasum) available" >&2; return 1; fi
}

# ---------------------------------------------------------------------------
# 1. ONNX Runtime (CPU) — per-OS/arch shared library
# ---------------------------------------------------------------------------
ORT_VERSION="${ORT_VERSION:-1.26.0}"
ORT_DIR="$MODELS/onnxruntime"
# Apple-Silicon-under-Rosetta returns Darwin-x86_64 from uname -m. Detect the
# real ARM hardware via sysctl (hw.optional.arm64=1) so a developer in an x86_64
# shell on an M-series Mac still gets the arm64 ORT instead of "Intel unsupported".
arch_uname="$(uname -m)"
if [[ "$(uname -s)" == "Darwin" && "$arch_uname" == "x86_64" ]] \
   && [[ "$(sysctl -in hw.optional.arm64 2>/dev/null)" == "1" ]]; then
  arch_uname="arm64"
fi
case "$(uname -s)-${arch_uname}" in
  Linux-x86_64)  ORT_PKG="onnxruntime-linux-x64-${ORT_VERSION}";     ORT_LIBNAME="libonnxruntime.so" ;;
  Linux-aarch64) ORT_PKG="onnxruntime-linux-aarch64-${ORT_VERSION}"; ORT_LIBNAME="libonnxruntime.so" ;;
  Darwin-arm64)  ORT_PKG="onnxruntime-osx-arm64-${ORT_VERSION}";     ORT_LIBNAME="libonnxruntime.dylib" ;;
  # Darwin-x86_64 (real Intel Mac): Microsoft stopped publishing
  # onnxruntime-osx-x86_64 at 1.25.0 (no upstream tarball) — stays LEXICAL-ONLY.
  *) echo "unsupported platform: $(uname -s)-${arch_uname} (Intel Mac unsupported since ORT 1.25)" >&2; exit 1 ;;
esac
if [[ ! -f "$ORT_DIR/lib/$ORT_LIBNAME" ]]; then
  echo "→ ONNX Runtime $ORT_VERSION ($ORT_PKG)"
  # ORT is dlopen'd native code → an unverified tarball is an RCE vector. Pin the
  # sha256 per package (must match internal/bootstrap/models.go) and fail closed.
  case "$ORT_PKG" in
    onnxruntime-linux-x64-1.26.0)     ORT_SHA=1254da24fb389cf39dc0ff3451ab48301740ffbfcbaf646849df92f80ee92c57 ;;
    onnxruntime-linux-aarch64-1.26.0) ORT_SHA=34ff1c2d0f12e2cf3d33a0c5f82e39792e1d581fbd6968fd7c30d173654be01a ;;
    onnxruntime-osx-arm64-1.26.0)     ORT_SHA=7a1280bbb1701ea514f71828765237e7896e0f2e1cd332f1f70dbd5c3e33aca3 ;;
    *) echo "no pinned ORT checksum for $ORT_PKG (refusing an unverified native runtime)" >&2; exit 1 ;;
  esac
  url="https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/${ORT_PKG}.tgz"
  tmp="$(mktemp -d)"
  dl "$url" "$tmp/ort.tgz"
  sha256_verify "$ORT_SHA" "$tmp/ort.tgz" || { echo "ORT checksum mismatch — refusing" >&2; rm -rf "$tmp"; exit 1; }
  tar -xzf "$tmp/ort.tgz" -C "$tmp"
  rm -rf "$ORT_DIR"; mv "$tmp/$ORT_PKG" "$ORT_DIR"
  rm -rf "$tmp"
fi

# ---------------------------------------------------------------------------
# 2. BGE-M3 dense embedder (ONNX export with EXTERNAL weights)
#    model.onnx is only the graph (~0.7 MB); the 2.2 GB of weights live in
#    model.onnx_data, plus one constant tensor in Constant_7_attr__value. All
#    three MUST sit side by side or the session fails to load. This is the bug
#    the previous version had: it fetched only model.onnx.
# ---------------------------------------------------------------------------
BGE_DIR="$MODELS/bge-m3"
# Pinned to an immutable commit SHA (not /resolve/main) — must match the Go
# bootstrap pins in internal/bootstrap/models.go (supply-chain reproducibility).
BGE_BASE="https://huggingface.co/BAAI/bge-m3/resolve/5617a9f61b028005a4858fdac845db406aefb181"
mkdir -p "$BGE_DIR"
echo "→ BGE-M3 ONNX + external weights (~2.3 GB) from HuggingFace"
dl "$BGE_BASE/onnx/model.onnx"             "$BGE_DIR/model.onnx"
dl "$BGE_BASE/onnx/model.onnx_data"        "$BGE_DIR/model.onnx_data"
dl "$BGE_BASE/onnx/Constant_7_attr__value" "$BGE_DIR/Constant_7_attr__value"
dl "$BGE_BASE/tokenizer.json"              "$BGE_DIR/tokenizer.json"

# ---------------------------------------------------------------------------
# 3. (optional) Cross-encoder reranker (bge-reranker-v2-m3).
#    BAAI ships NO official ONNX export (PyTorch only), so we default to the
#    celinehoang fp32 ONNX export (graph + external weights), verified to have
#    inputs input_ids/attention_mask -> output logits and to load under ORT 1.20.
#    Override RERANKER_ONNX_URL / RERANKER_DATA_URL to use a different export.
# ---------------------------------------------------------------------------
if [[ "${WITH_RERANKER:-0}" == "1" ]]; then
  RR_DIR="$MODELS/bge-reranker-v2-m3"
  RR_BASE="https://huggingface.co/celinehoang/bge-reranker-v2-m3-onnx/resolve/87449985a27bbd817f13ee2338df130bdb532bad"
  RERANKER_ONNX_URL="${RERANKER_ONNX_URL:-$RR_BASE/model.onnx}"
  RERANKER_DATA_URL="${RERANKER_DATA_URL:-$RR_BASE/model.onnx_data}"
  mkdir -p "$RR_DIR"
  echo "→ bge-reranker-v2-m3 ONNX + weights (~2.3 GB)"
  dl "$RERANKER_ONNX_URL" "$RR_DIR/model.onnx"
  dl "$RERANKER_DATA_URL" "$RR_DIR/model.onnx_data"
  dl "https://huggingface.co/BAAI/bge-reranker-v2-m3/resolve/953dc6f6f85a1b2dbfca4c34a2796e7dde08d41e/tokenizer.json" "$RR_DIR/tokenizer.json"
fi

# ---------------------------------------------------------------------------
# 4. (optional) fp16 BGE-M3 export for the T4 Tensor-Core path (model "bge-m3-fp16"
#    in the embed registry). We do NOT pin a default fp16 source: converting fp32→
#    fp16 needs an offline toolchain (out of this repo's scope), and a community
#    fp16 export must be VALIDATED before it becomes a permanent 10M index. So the
#    operator supplies FP16_ONNX_URL (+ FP16_DATA_URL for external weights). The
#    registry then exposes it as EMBED_MODEL=bge-m3-fp16; the acceptance gate
#    (go test -tags onnx -run TestFP16GateVsFP32, cos>=0.9995) must pass first.
# ---------------------------------------------------------------------------
if [[ "${WITH_FP16:-0}" == "1" ]]; then
  FP16_DIR="$MODELS/bge-m3-fp16"
  if [[ -z "${FP16_ONNX_URL:-}" ]]; then
    echo "WITH_FP16=1 but FP16_ONNX_URL is unset." >&2
    echo "  Supply a VALIDATED fp16 BGE-M3 dense ONNX export, e.g.:" >&2
    echo "    FP16_ONNX_URL=<...model.onnx> FP16_DATA_URL=<...model.onnx_data> WITH_FP16=1 $0" >&2
    echo "  Then gate it: EMBED_MODELS_BASE=\$PWD go test -tags onnx ./internal/embed -run TestFP16GateVsFP32" >&2
    exit 2
  fi
  mkdir -p "$FP16_DIR"
  echo "→ fp16 BGE-M3 ONNX (operator-supplied, must pass the cos>=0.9995 gate)"
  dl "$FP16_ONNX_URL" "$FP16_DIR/model.onnx"
  [[ -n "${FP16_DATA_URL:-}" ]] && dl "$FP16_DATA_URL" "$FP16_DIR/model.onnx_data"
  # tokenizer is precision-independent; the registry's bge-m3-fp16 entry points its
  # tokenizer_dir at the fp32 dir, so no separate tokenizer fetch is required.
  echo "  fp16 ready: EMBED_MODEL=bge-m3-fp16 (validate with TestFP16GateVsFP32 before any bulk run)"
fi

# ---------------------------------------------------------------------------
# 5. (optional) BGE-M3 export WITH the learned-lexical (sparse) head — model
#    "bge-m3-sparse" in the embed registry (declares sparse_output). This is the
#    one model prerequisite that lights up the production sparse arm. BAAI ships no
#    sparse ONNX, so it is produced by scripts/export-bge-m3-sparse.py (torch +
#    FlagEmbedding) OR fetched from an operator-supplied, validated URL. Activating
#    it is ADDITIVE: it only triggers an `embed --sparse-only` pass over existing
#    clauses — the dense vectors are never recomputed.
# ---------------------------------------------------------------------------
if [[ "${WITH_SPARSE:-0}" == "1" ]]; then
  SP_DIR="$MODELS/bge-m3-sparse"
  mkdir -p "$SP_DIR"
  if [[ -n "${SPARSE_ONNX_URL:-}" ]]; then
    echo "→ bge-m3-sparse ONNX (operator-supplied, must expose [sentence_embedding, sparse_weights])"
    dl "$SPARSE_ONNX_URL" "$SP_DIR/model.onnx"
    [[ -n "${SPARSE_DATA_URL:-}" ]] && dl "$SPARSE_DATA_URL" "$SP_DIR/model.onnx_data"
  elif command -v python3 >/dev/null 2>&1; then
    echo "→ exporting bge-m3-sparse with scripts/export-bge-m3-sparse.py (torch + FlagEmbedding required)"
    python3 "$ROOT/scripts/export-bge-m3-sparse.py" --out "$SP_DIR/model.onnx"
  else
    echo "WITH_SPARSE=1 but neither SPARSE_ONNX_URL nor python3 is available." >&2
    echo "  Supply SPARSE_ONNX_URL=<...model.onnx> [SPARSE_DATA_URL=<...model.onnx_data>] WITH_SPARSE=1 $0" >&2
    echo "  or run scripts/export-bge-m3-sparse.py on a box with torch+FlagEmbedding." >&2
    exit 2
  fi
  # The tokenizer is precision/head-independent; the registry's bge-m3-sparse entry
  # points its tokenizer_dir at the dense bge-m3 dir, so no separate fetch is needed.
  echo "  sparse model ready. Make it active (models.yaml: sparse_output: sparse_weights),"
  echo "  then populate ADDITIVELY (no dense re-embed): embed --db <fused.duckdb> --sparse-only"
fi

echo "✓ models ready in $MODELS"
echo "  ORT lib : $ORT_DIR/lib/$ORT_LIBNAME"
echo "  build   : make build-onnx"
echo "  ingest  : make ingest-onnx ARGS=\"--spec 33.128\""
