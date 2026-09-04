#!/usr/bin/env bash
#
# restore-bge-sparse.sh DEST_DIR — materialise the dual-head BGE-M3 export at
# DEST_DIR/bge-m3-sparse (dense sentence_embedding + the learned-lexical
# sparse_weights head).
#
# WHY IT IS A SEPARATE SCRIPT FROM restore-bge.sh, AND WHY IT HAS NO FALLBACK.
# restore-bge.sh ends at HuggingFace when the release asset is missing. There is
# no such fallback here: BAAI publishes no sparse ONNX for BGE-M3, so this file
# does not exist anywhere upstream. It is produced once, by
# scripts/export-bge-m3-sparse.py on a machine with torch + FlagEmbedding, and
# then lives in this repository's own `models` release beside bge-m3 and the
# reranker.
#
# So a missing asset is a HARD FAILURE, deliberately. Skipping it quietly would
# bake an image whose active model is dense-only, which drops the sparse arm at
# serve time (SparseCapable() reads the active registry entry) while the corpus
# still carries every sparse posting the GPU pass paid for. That image would
# answer, plausibly, with one retrieval arm silently missing.
#
# To PRODUCE and publish the asset from a machine that has the model:
#
#   scripts/export-bge-m3-sparse.py --out data/models/bge-m3-sparse/model.onnx
#   cp data/models/bge-m3/tokenizer.json data/models/bge-m3-sparse/
#   scripts/publish-model-asset.sh bge-m3-sparse data/models/bge-m3-sparse
#
# Needs: gh (authenticated via GH_TOKEN), zstd, sha256sum. Arch-independent.
set -euo pipefail

DEST="${1:?usage: restore-bge-sparse.sh DEST_DIR}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The same weights pin as the dense export: this IS BGE-M3, exported with both
# heads, so the revision that identifies the dense model identifies this one.
# Single-sourced from the Go pin for the same reason restore-bge.sh is.
COMMIT="$(grep -oE 'BGECommit = "[0-9a-f]{40}"' "$ROOT/internal/bootstrap/models.go" | grep -oE '[0-9a-f]{40}')"
[ -n "$COMMIT" ] || { echo "[restore-bge-sparse] cannot read BGECommit from internal/bootstrap/models.go" >&2; exit 1; }
ASSET="bge-m3-sparse-${COMMIT:0:8}.tar.zst"
mkdir -p "$DEST"

if [ -s "$DEST/bge-m3-sparse/model.onnx.data" ] || [ -s "$DEST/bge-m3-sparse/model.onnx_data" ]; then
  echo "[restore-bge-sparse] $DEST/bge-m3-sparse already present — nothing to do"
  exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if ! gh release download models --repo "${GITHUB_REPOSITORY:-kodflow/3gpp-mcp}" \
       --pattern "${ASSET}.part*" --pattern "${ASSET}.sha256" -D "$tmp" 2>/dev/null; then
  cat >&2 <<EOF
[restore-bge-sparse] FATAL: $ASSET is not in the models release, and there is no
  upstream to fall back to — BAAI publishes no sparse ONNX for BGE-M3.

  Baking without it produces an image that carries the corpus's sparse postings
  and cannot query them: the active model would be dense-only, SparseCapable()
  false, and search.Engine drops the learned-lexical arm without an error.

  Produce and publish it once, from a machine with torch + FlagEmbedding:
    scripts/export-bge-m3-sparse.py --out data/models/bge-m3-sparse/model.onnx
    cp data/models/bge-m3/tokenizer.json data/models/bge-m3-sparse/
    scripts/publish-model-asset.sh bge-m3-sparse data/models/bge-m3-sparse
EOF
  exit 1
fi

cat "$tmp/${ASSET}".part* > "$tmp/$ASSET"
if ! (cd "$tmp" && sha256sum -c "$ASSET.sha256" >/dev/null 2>&1); then
  echo "[restore-bge-sparse] FATAL: $ASSET failed its sha256 — refusing to bake a corrupt model" >&2
  exit 1
fi
tar -C "$DEST" --use-compress-program=unzstd -xf "$tmp/$ASSET"
echo "[restore-bge-sparse] restored $ASSET from the models release"
