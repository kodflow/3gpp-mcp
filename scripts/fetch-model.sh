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
# Then:    make build-onnx
#          make ingest-onnx ARGS="--spec 33.128"
#
# Env overrides:
#   ORT_VERSION   ONNX Runtime version  (default 1.20.1 — matches onnxruntime_go v1.14.0 / API 20)
#   MODELS        target dir            (default <repo>/data/models)
#   WITH_RERANKER fetch the reranker    (default 0; source resolved in A3)
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

echo "✓ models ready in $MODELS"
echo "  ORT lib : $ORT_DIR/lib/$ORT_LIBNAME"
echo "  build   : make build-onnx"
echo "  ingest  : make ingest-onnx ARGS=\"--spec 33.128\""
