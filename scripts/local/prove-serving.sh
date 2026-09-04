#!/usr/bin/env bash
# Final end-to-end proof, over REAL JSON-RPC against the finished corpus.
#
# WHY THIS EXISTS BESIDE `smoke`. The goal step `smoke` drives
# .local/bin/server.exe — the LEXICAL build — so it can assert what the CORPUS
# offers (fts, hnsw) but never that a query can actually be embedded. This drives
# server-full.exe (onnx + embed_ffi), the binary the image carries, with the
# environment that binary needs, and asserts on what server_info answers.
#
# The assertions live in assert.sh so they can be run against a saved transcript
# without starting a server — which is how they were falsified: on a lexical
# transcript it reports fts x2 and ETSI attached, and MISSING for every other arm.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
. scripts/local/toolchain-env.sh >/dev/null 2>&1
export EMBED_MODEL=bge-m3-sparse
export EMBED_MODEL_DIR="$ROOT/data/models/bge-m3-sparse"
export BGE_RERANKER_DIR="$ROOT/data/models/bge-reranker-v2-m3"
# TWO BINDINGS, TWO RUNTIMES, AND THEY ARE NOT INTERCHANGEABLE.
#
# ORT_DYLIB_PATH is the RUST crate's variable: it is what rust/embed-core loads,
# and it points at the GPU 1.20.1 build the embedder was compiled against.
# ONNXRUNTIME_SHARED_LIBRARY_PATH is the GO binding's, used by the reranker.
#
# Setting only the first left the cross-encoder reporting
#
#   "reranker_reason": "the ONNX runtime would not initialise:
#                       Platform-specific initialization failed:
#                       Error setting ORT API base: 2"
#
# — OrtGetApiBase refusing the API version yalue/onnxruntime_go asks for, because
# 1.20.1 predates it. One file cannot satisfy both pins, and the process does not
# need it to: measured 2026-09-01, with the two variables pointing at 1.20.1 and
# the pinned 1.26.0 respectively, semantic=true and reranker=true came back
# together from the same server.
#
# 1.26.0 is what the image stages (scripts/local/build-image.sh reads the version
# from scripts/fetch-model.sh), so pointing the Go side at it here proves the arm
# against the SAME runtime version the image serves — the OS differs, the ABI does
# not.
ORT="$ROOT/.local/toolchain/ort/onnxruntime-win-x64-gpu-1.20.1/lib"
export ORT_DYLIB_PATH="$ORT/onnxruntime.dll"
export PATH="$ORT:$PATH"
if [ -z "${ONNXRUNTIME_SHARED_LIBRARY_PATH:-}" ]; then
  for cand in "$ROOT/data/models/onnxruntime/lib/onnxruntime.dll"               "$ROOT/data/models/onnxruntime/lib/libonnxruntime.so"; do
    [ -f "$cand" ] && { export ONNXRUNTIME_SHARED_LIBRARY_PATH="$cand"; break; }
  done
fi
if [ -z "${ONNXRUNTIME_SHARED_LIBRARY_PATH:-}" ]; then
  echo "NOTE: no ONNX Runtime for the Go binding under data/models/onnxruntime/lib." >&2
  echo "      The cross-encoder will report why it is off, and this proof will FAIL on it." >&2
  echo "      Put the official build of the pinned version there (the same one the image" >&2
  echo "      stages) — it is a different pin from the Rust crate's ORT_DYLIB_PATH." >&2
fi

OUT="$ROOT/.local/prove.out"
ERR="$ROOT/.local/prove.stderr"

{
  echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"prove","version":"1"}}}'
  echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
  echo '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"server_info","arguments":{}}}'
  echo '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_spec","arguments":{"query":"AMF registration procedure over N1","mode":"semantic","limit":3}}}'
  echo '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_spec","arguments":{"query":"lawful interception X1 ADMF task activation","spec_type":"any","limit":5}}}'
  echo '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"trace_evolution","arguments":{"term":"MME"}}}'
} | .local/bin/server-full.exe serve -no-update --db data/3gpp.duckdb --etsi-db data/etsi.duckdb >"$OUT" 2>"$ERR"

echo "===== server_info ====="
grep -o '"id":2.*' "$OUT" | head -c 1800; echo
echo
bash "$ROOT/scripts/local/assert-serving.sh" "$OUT"
rc=$?

echo
echo "===== stderr (boot lines) ====="
head -20 "$ERR"
if grep -q "semantic disabled\|degraded to 3GPP-only" "$ERR"; then
  echo "PROVE: the server degraded at startup — see $ERR" >&2
  rc=1
fi

[ "$rc" -eq 0 ] || { echo "PROVE FAILED — transcript in $OUT" >&2; exit 1; }
echo "PROVE OK — every arm live on both halves, over real JSON-RPC"
