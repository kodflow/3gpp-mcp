#!/usr/bin/env bash
#
# light-artifacts.sh — from a LEXICAL (no-embed) DuckDB, emit the TWO .zst the
# pipeline ships each cycle:
#
#   1. <DIST>/3gpp.duckdb.zst   the full lexical DB  → the (private) Release asset
#                               + the iteration baseline for the next delta.
#   2. <DIST>/3gpp.delta.zst    the embedding work-list (clauses WHERE embedding
#                               IS NULL, text only) → the input pushed to the GPU.
#
# On a fresh lexical DB every clause is NULL, so the delta is the whole corpus;
# on an incremental re-ingest it is only what moved. The full DB.zst is produced
# regardless of embed state ("ça ne change pas").
#
#   LEX=data/3gpp.lexical.duckdb scripts/light-artifacts.sh
#   LEX=… DIST=dist SERIES=23 scripts/light-artifacts.sh   # scope the delta
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LEX="${LEX:-$ROOT/data/3gpp.lexical.duckdb}"
DIST="${DIST:-$ROOT/dist}"
GO="${GO:-go}"
SERIES="${SERIES:-}"

log() { echo "[light] $*"; }
[ -s "$LEX" ] || { echo "[light] missing lexical DB: $LEX (run: merge --strip-embeddings)"; exit 1; }
command -v zstd >/dev/null || { echo "[light] zstd not installed"; exit 1; }
mkdir -p "$DIST"

# 1. full lexical DB → db.zst (high ratio + long window, matches the rest of the pipeline)
full_zst="$DIST/3gpp.duckdb.zst"
log "compress full lexical DB → $full_zst"
zstd -19 --long=27 -f -q "$LEX" -o "$full_zst"

# 2. delta DB (clauses needing embedding) + its .zst, via cmd/export-delta
delta_db="$DIST/3gpp.delta.duckdb"
series_args=()
[ -n "$SERIES" ] && series_args=(--series "$SERIES")
log "export delta (embedding IS NULL${SERIES:+, series $SERIES}) → $delta_db(.zst)"
CGO_ENABLED=1 "$GO" run "$ROOT/cmd/export-delta" --db "$LEX" --out "$delta_db" --zstd "${series_args[@]}"

log "artifacts:"
ls -lh "$full_zst" "$delta_db.zst" 2>/dev/null | sed 's/^/[light]   /'
log "done."
