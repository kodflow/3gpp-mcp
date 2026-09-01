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
ORT="$ROOT/.local/toolchain/ort/onnxruntime-win-x64-gpu-1.20.1/lib"
export ORT_DYLIB_PATH="$ORT/onnxruntime.dll"
export PATH="$ORT:$PATH"

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
