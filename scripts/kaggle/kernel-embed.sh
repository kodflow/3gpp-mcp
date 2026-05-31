#!/usr/bin/env bash
#
# kernel-embed.sh — the body of the Kaggle kernel (notebook) that runs ON the
# free T4 GPU. It is pushed by scripts/kaggle-embed-poc.sh (local driver) and
# executed by Kaggle's runner; it is NOT meant to run on the dev laptop.
#
# Contract (PR-11a POC):
#   inputs   (Kaggle dataset, mounted read-only under /kaggle/input/<slug>/):
#              - 3gpp.duckdb            a small LEXICAL DB (one real series, e.g. 33)
#              - bge-m3/               the BGE-M3 ONNX model + tokenizer.json
#              - onnxruntime-gpu/lib/  the CUDA-enabled ONNX Runtime shared lib
#              - src.tar               this repo's source (no creds, no data/)
#   working  (/kaggle/working/, the only writable + downloadable dir):
#              - 3gpp.duckdb            the embedded DB pulled back by the driver
#   output   asserts null_embeddings_at_floor==0 at the requested floor and
#            prints clauses/s so the driver can compute the T4-vs-CPU speedup.
#
# The kernel toggles "Internet" + "GPU T4 x2" in its metadata (set by the
# driver). Go is installed on-box if absent. NO Kaggle credentials are read or
# needed here — auth happens entirely on the laptop.
set -euo pipefail

FLOOR="${EMBED_FLOOR:-Rel-15}"
IN="$(echo /kaggle/input/*/ | awk '{print $1}')" # the single mounted dataset
WORK=/kaggle/working
echo "[kernel] input dataset : $IN"
echo "[kernel] embed floor   : $FLOOR"
nvidia-smi || { echo "[kernel] no GPU visible — aborting"; exit 1; }

# 1. Go toolchain (Kaggle images vary; install a pinned Go if missing).
if ! command -v go >/dev/null 2>&1; then
  GOVER=1.23.4
  echo "[kernel] installing Go $GOVER"
  curl -fsSL "https://go.dev/dl/go${GOVER}.linux-amd64.tar.gz" -o /tmp/go.tgz
  tar -C /usr/local -xzf /tmp/go.tgz
  export PATH=/usr/local/go/bin:$PATH
fi
go version

# 2. Unpack the repo source shipped in the dataset and build the embed binary
#    with the onnx tag (CGO). The CUDA-enabled ORT lib comes from the dataset.
SRC="$WORK/src"
mkdir -p "$SRC"
tar -C "$SRC" -xf "$IN/src.tar"
cd "$SRC"

ORT_LIB="$IN/onnxruntime-gpu/lib/libonnxruntime.so"
ORT_DIR="$(dirname "$ORT_LIB")"
export ONNXRUNTIME_SHARED_LIBRARY_PATH="$ORT_LIB"
export CGO_ENABLED=1
export CGO_LDFLAGS="-L$ORT_DIR -lonnxruntime"
export LD_LIBRARY_PATH="$ORT_DIR:${LD_LIBRARY_PATH:-}"
go build -tags onnx -o "$WORK/embed" ./cmd/embed

# 3. Copy the lexical DB into the writable dir and embed it on the GPU.
cp "$IN/3gpp.duckdb" "$WORK/3gpp.duckdb"
export BGE_M3_DIR="$IN/bge-m3"
export ORT_EP=cuda # the only knob PR-11a adds — selects the CUDA execution provider

START=$(date +%s)
REPORT="$WORK/embed-report.json"
"$WORK/embed" --db "$WORK/3gpp.duckdb" --embed-floor "$FLOOR" \
  --require-semantic --report json | tee "$REPORT"
END=$(date +%s)
ELAPSED=$((END - START))

# 4. Assert full coverage at the floor and print throughput.
NULLS=$(grep -o '"null_embeddings_at_floor":[0-9]*' "$REPORT" | head -1 | cut -d: -f2)
EMBEDDED=$(grep -o '"embedded":[0-9]*' "$REPORT" | head -1 | cut -d: -f2 || echo 0)
echo "[kernel] elapsed=${ELAPSED}s embedded=${EMBEDDED} null_at_floor=${NULLS:-?}"
if [ "${NULLS:-1}" != "0" ]; then
  echo "[kernel] FAIL: null_embeddings_at_floor=${NULLS:-?} (want 0)"; exit 1
fi
if [ "${ELAPSED}" -gt 0 ] && [ "${EMBEDDED:-0}" -gt 0 ]; then
  awk -v e="$EMBEDDED" -v t="$ELAPSED" 'BEGIN{printf "[kernel] throughput: %.2f clauses/s (T4)\n", e/t}'
fi
echo "[kernel] OK — embedded DB at $WORK/3gpp.duckdb (pull via kaggle kernels output)"
