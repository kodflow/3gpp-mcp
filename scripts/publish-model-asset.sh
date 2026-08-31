#!/usr/bin/env bash
#
# publish-model-asset.sh NAME SRC_DIR — pack a model directory and upload it to
# this repository's `models` release, in the shape restore-bge*.sh expects:
#
#   <NAME>-<pin>.tar.zst.part00, .part01, …   (split: GitHub caps an asset at 2 GiB)
#   <NAME>-<pin>.tar.zst.sha256               (over the REASSEMBLED archive)
#
# The `models` release is the durable cache that keeps HuggingFace out of the CI
# hot path — it 429-rate-limited the runners three times in one day — and it is
# the ONLY source for models that have no upstream at all, like the dual-head
# bge-m3-sparse export (BAAI publishes no sparse ONNX).
#
# The pin is the BGE weights commit, single-sourced from internal/bootstrap/
# models.go exactly as the restore scripts read it, so a producer and a consumer
# cannot disagree about which file they mean.
#
# Idempotent-ish: --clobber replaces same-named assets, so re-running after a
# failed upload finishes the job rather than erroring on the parts that landed.
#
# Needs: gh (authenticated, with contents:write), tar, zstd, split, sha256sum.
set -euo pipefail

NAME="${1:?usage: publish-model-asset.sh NAME SRC_DIR}"
SRC="${2:?usage: publish-model-asset.sh NAME SRC_DIR}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO="${GITHUB_REPOSITORY:-kodflow/3gpp-mcp}"

[ -d "$SRC" ] || { echo "[publish-model] $SRC is not a directory" >&2; exit 1; }

COMMIT="$(grep -oE 'BGECommit = "[0-9a-f]{40}"' "$ROOT/internal/bootstrap/models.go" | grep -oE '[0-9a-f]{40}')"
[ -n "$COMMIT" ] || { echo "[publish-model] cannot read BGECommit from internal/bootstrap/models.go" >&2; exit 1; }
ASSET="${NAME}-${COMMIT:0:8}.tar.zst"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Pack the directory UNDER ITS OWN NAME: the restore scripts untar into a
# destination and expect DEST/<NAME>/ to appear, so the archive must carry that
# leading component. Packing the contents alone would scatter the model files
# into the destination root and every consumer would look in the wrong place.
echo "[publish-model] packing $SRC as $NAME/ …"
tar -C "$(dirname "$SRC")" --use-compress-program='zstd -19 -T0' -cf "$tmp/$ASSET" "$(basename "$SRC")"

( cd "$tmp" && sha256sum "$ASSET" > "$ASSET.sha256" )
echo "[publish-model] $ASSET $(du -h "$tmp/$ASSET" | cut -f1)"

# 1900 MiB parts: under GitHub's 2 GiB asset cap with room for the size to be
# reported in whichever unit the API happens to use.
( cd "$tmp" && split -b 1900m -d -a 2 "$ASSET" "$ASSET.part" && rm -f "$ASSET" )

echo "[publish-model] uploading $(ls "$tmp/$ASSET".part* | wc -l) part(s) + checksum to the models release of $REPO"
gh release upload models --repo "$REPO" --clobber "$tmp/$ASSET".part* "$tmp/$ASSET.sha256"
echo "[publish-model] done: $ASSET"
