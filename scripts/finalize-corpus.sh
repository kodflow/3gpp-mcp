#!/usr/bin/env bash
#
# finalize-corpus.sh — ONE command to turn the finished Kaggle lots into a usable DB.
# Runs where the Kaggle token + the lot outputs live. Designed for the "I woke up,
# make it functional" moment, and for the unattended Monitor-triggered run.
#
#   KAGGLE_USER=<handle> scripts/finalize-corpus.sh                # collect+merge+validate
#   KAGGLE_USER=<handle> scripts/finalize-corpus.sh --image        # + bake the runtime image
#
# Steps: 1) collect the two lot outputs (skip if already present), 2) merge into one
# embedded DB (vectors preserved + FTS), 3) validate completeness, 4) optionally bake
# the Docker image with the DB.
#
# ⚠ MEMORY: building the HNSW index over the full corpus (~2.45M × 1024 vectors) needs
# a lot of RAM. On a low-RAM box set MERGE_HNSW=0 (default auto-detect: <24GB RAM → skip
# HNSW). A DB without HNSW still serves vector search by EXACT SCAN (correct, slower) and
# HNSW can be frozen later on a bigger machine via `embed --db DB` (build-only) or merge.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STAGE="${STAGE:-$ROOT/.kaggle-lots}"
OUT="${OUT:-$ROOT/data/3gpp.duckdb}"
GO="${GO:-go}"
# 18 releases of the current corpus (Rel-99 + Rel-4..Rel-20); the validate gate checks them.
EXPECTED_RELEASES="${EXPECTED_RELEASES:-Rel-99,Rel-4,Rel-5,Rel-6,Rel-7,Rel-8,Rel-9,Rel-10,Rel-11,Rel-12,Rel-13,Rel-14,Rel-15,Rel-16,Rel-17,Rel-18,Rel-19,Rel-20}"
MIN_CLAUSES="${MIN_CLAUSES:-2000000}"

# HNSW only if plenty of RAM (build is memory-hungry); else skip (exact-scan vectors).
ram_gb=$(awk '/MemTotal/{print int($2/1024/1024)}' /proc/meminfo 2>/dev/null || echo 0)
MERGE_HNSW="${MERGE_HNSW:-$([ "$ram_gb" -ge 24 ] && echo 1 || echo 0)}"

log() { echo "[finalize] $*"; }
WANT_IMAGE=0; [ "${1:-}" = "--image" ] && WANT_IMAGE=1

# 1. collect (skip if both partials already present + non-trivial)
a="$STAGE/out-A/3gpp-embedded.duckdb"; b="$STAGE/out-B/3gpp-embedded.duckdb"
if [ ! -s "$a" ] || [ ! -s "$b" ]; then
  log "collecting lot outputs from Kaggle"
  KAGGLE_USER="${KAGGLE_USER:?set KAGGLE_USER}" "$ROOT/scripts/kaggle-embed-lots.sh" collect
fi
[ -s "$a" ] && [ -s "$b" ] || { echo "missing lot DBs ($a / $b)"; exit 1; }

# 2. assemble the full DB: the lot outputs are CLAUSES-ONLY (vectors, no catalogue), so
# we OVERLAY their vectors onto the full lexical `latest` base (catalogue + clauses, no
# vectors) keyed by chunk_id. NOT `merge` (merging the clauses-only lots loses the
# specs/spec_versions/acronyms/changes/api/li catalogue). HNSW is not built (RAM); serve
# does exact-scan vector search (build HNSW later on a big-RAM box).
mkdir -p "$(dirname "$OUT")"
LEX_URL="${LEX_URL:-https://github.com/kodflow/3gpp-mcp/releases/download/latest/3gpp.duckdb.zst}"
log "pull lexical base (catalogue) ← $LEX_URL"
curl -fsSL --retry 5 -o "$OUT.lex.zst" "$LEX_URL"
zstd -d --long=27 -f "$OUT.lex.zst" -o "$OUT"; rm -f "$OUT.lex.zst"
log "overlay lot vectors onto the lexical base → $OUT"
CGO_ENABLED=1 "$GO" run "$ROOT/cmd/overlay" --base "$OUT" --vec "$a" --vec "$b"

# 3. validate (gate). pending>0 ⇒ incomplete (e.g. a lot did not fully embed) → WARN.
log "validate"
req_hnsw=""; [ "$MERGE_HNSW" = 1 ] && req_hnsw="--require-hnsw"
if CGO_ENABLED=1 "$GO" run "$ROOT/cmd/validate" --db "$OUT" \
    --pending-zero --require-fts $req_hnsw \
    --expected-releases "$EXPECTED_RELEASES" --min-clauses "$MIN_CLAUSES"; then
  log "VALIDATED ✓ — $OUT is complete"
else
  log "⚠ validation FAILED — DB is usable but incomplete (likely pending embeddings). Inspect above."
fi

# 4. optional: bake the runtime image with the DB (PRIVATE — full 3GPP text inside)
if [ "$WANT_IMAGE" = 1 ]; then
  log "baking runtime image 3gpp-mcp:local (DB baked — keep PRIVATE)"
  mkdir -p "$ROOT/image-data"; cp "$OUT" "$ROOT/image-data/3gpp.duckdb"
  docker build -t 3gpp-mcp:local "$ROOT"
  rm -f "$ROOT/image-data/3gpp.duckdb"
  log "image built: 3gpp-mcp:local  (run: docker run -i --rm 3gpp-mcp:local serve  |  or -e MCP_TRANSPORT=http -p 8765:8765)"
fi
log "done."
