#!/usr/bin/env bash
#
# restore-bge.sh DEST_DIR — materialise the pinned BGE-M3 model at DEST_DIR/bge-m3.
#
# Source order (the whole point is to keep HuggingFace OUT of the hot path —
# it 429-rate-limited the CI runners three times in one day):
#   1. the repo's own 'models' GitHub release (the durable cache corpus-matrix's
#      model job maintains: <2GiB .partNN splits + whole-archive sha256);
#   2. HuggingFace, pinned commit, with the shared retry policy — at most ONE
#      fetch, and only when the release asset is absent or fails its checksum.
#
# The pinned commit is read from the Go source (internal/bootstrap/models.go
# BGECommit) so there is no second copy of the pin to drift. Idempotent: a
# DEST_DIR/bge-m3 that already holds the weights is kept as-is.
#
# Needs: gh (authenticated via GH_TOKEN), zstd, curl. Arch-independent output.
set -euo pipefail

DEST="${1:?usage: restore-bge.sh DEST_DIR}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=lib/retry.sh
source "$ROOT/scripts/lib/retry.sh"

COMMIT="$(grep -oE 'BGECommit = "[0-9a-f]{40}"' "$ROOT/internal/bootstrap/models.go" | grep -oE '[0-9a-f]{40}')"
[ -n "$COMMIT" ] || { echo "[restore-bge] cannot read BGECommit from internal/bootstrap/models.go" >&2; exit 1; }
ASSET="bge-m3-${COMMIT:0:8}.tar.zst"
mkdir -p "$DEST"

if [ -s "$DEST/bge-m3/model.onnx_data" ]; then
  echo "[restore-bge] $DEST/bge-m3 already present — nothing to do"
  exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
if gh release download models --repo "${GITHUB_REPOSITORY:-kodflow/3gpp-mcp}" \
     --pattern "${ASSET}.part*" --pattern "${ASSET}.sha256" -D "$tmp" 2>/dev/null; then
  cat "$tmp/${ASSET}".part* > "$tmp/$ASSET"
  if (cd "$tmp" && sha256sum -c "$ASSET.sha256" >/dev/null 2>&1); then
    tar -C "$DEST" --use-compress-program=unzstd -xf "$tmp/$ASSET"
    echo "[restore-bge] restored from the models release ($ASSET) — zero HF traffic"
    exit 0
  fi
  echo "[restore-bge] WARNING: release archive failed sha256 — falling back to HF" >&2
else
  echo "[restore-bge] WARNING: $ASSET absent from the models release — falling back to HF" >&2
fi

mkdir -p "$DEST/bge-m3"
base="https://huggingface.co/BAAI/bge-m3/resolve/$COMMIT"
for f in onnx/model.onnx onnx/model.onnx_data onnx/Constant_7_attr__value tokenizer.json; do
  retry curl -fsSL "$base/$f" -o "$DEST/bge-m3/$(basename "$f")"
done
echo "[restore-bge] fetched from HuggingFace (pinned ${COMMIT:0:8})"
