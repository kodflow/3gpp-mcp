#!/usr/bin/env bash
#
# publish-per-release.sh — split the embedded lot DBs into ONE DB per 3GPP release
# and publish each as its own GitHub Release asset. `latest` stays binary-only
# (the GoReleaser release.yml owns it); this script never touches `latest`.
#
# Pipeline (run where the Kaggle token + the collected lot DBs live):
#   scripts/kaggle-embed-lots.sh collect          # pulls .kaggle-lots/out-A|B/3gpp-embedded.duckdb
#   scripts/publish-per-release.sh                 # split → per-release → gh release
#
# Each 3GPP release becomes a Release tagged lowercase (Rel-18→rel-18, Phase1→phase1)
# carrying ONLY 3gpp-<release>.duckdb.zst (+ .sha256). The Corpus Image CI then pulls
# them all back and re-merges (cmd/merge rebuilds FTS+HNSW) into the baked image DB.
#
# Env:
#   LOTS_DIR   collected lot dir (default .kaggle-lots)
#   DIST       per-release output dir (default dist)
#   DRAFT      "1" → create releases as drafts (review before going live)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOTS_DIR="${LOTS_DIR:-$ROOT/.kaggle-lots}"
DIST="${DIST:-$ROOT/dist}"

die() { echo "error: $*" >&2; exit 1; }
log() { echo "[publish-per-release] $*"; }

command -v gh   >/dev/null 2>&1 || die "the 'gh' CLI is not installed/authenticated"
command -v zstd >/dev/null 2>&1 || die "zstd is not installed"

# 1. locate the collected lot DBs.
lots=$(find "$LOTS_DIR" -name '3gpp-embedded.duckdb' 2>/dev/null || true)
[ -n "$lots" ] || die "no lot DBs under $LOTS_DIR — run 'scripts/kaggle-embed-lots.sh collect' first"

# 2. build the splitter and split every lot into per-release DBs (zstd sidecars).
log "building cmd/split"
CGO_ENABLED=1 go build -o "$ROOT/.bin-split" "$ROOT/cmd/split"
mkdir -p "$DIST"
while IFS= read -r db; do
  [ -n "$db" ] || continue
  log "splitting $db"
  "$ROOT/.bin-split" --db "$db" --out-dir "$DIST" --zstd
done <<EOF
$lots
EOF
rm -f "$ROOT/.bin-split"

# 3. one Release per per-release DB. Tag = lowercase release label.
shopt -s nullglob
published=0
for z in "$DIST"/3gpp-*.duckdb.zst; do
  base="$(basename "$z")"                 # 3gpp-Rel-18.duckdb.zst
  rel="${base#3gpp-}"; rel="${rel%.duckdb.zst}"   # Rel-18
  tag="$(printf '%s' "$rel" | tr '[:upper:]' '[:lower:]')"  # rel-18
  sha="$z.sha256"; sha256sum "$z" | awk -v f="$(basename "$z")" '{print $1"  "f}' > "$sha"
  log "publishing release '$tag' (from $rel)"
  draft_flag=""; [ "${DRAFT:-0}" = "1" ] && draft_flag="--draft"
  if gh release view "$tag" >/dev/null 2>&1; then
    gh release upload "$tag" "$z" "$sha" --clobber
  else
    # shellcheck disable=SC2086
    gh release create "$tag" "$z" "$sha" $draft_flag \
      --title "3GPP $rel corpus" \
      --notes "Embedded DuckDB for 3GPP $rel (no FTS/HNSW — rebuilt on merge by the Corpus Image CI)."
  fi
  published=$((published+1))
done
[ "$published" -gt 0 ] || die "no per-release DBs found in $DIST (did the split produce anything?)"
log "published $published per-release Release(s). 'latest' left untouched (binary-only)."
