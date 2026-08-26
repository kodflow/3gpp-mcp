#!/usr/bin/env bash
#
# status.sh — ou en est le corpus local. Lecture seule, aucun effet de bord.
#
set -uo pipefail
# shellcheck source=lib-local.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib-local.sh"
PHASE=status

sz() { [ -e "$1" ] && human "$(du -sb "$1" 2>/dev/null | cut -f1)" || printf '%s\n' "-"; }
row() { printf '  %-34s %s\n' "$1" "$2"; }

printf '\n\033[1m== SOURCES ==\033[0m\n'
row "zips d'origine (purgeables)" "$(sz "$SRC_ORIGIN") · $(find "$SRC_ORIGIN" -name '*.zip' 2>/dev/null | wc -l) fichiers"
row "HTML converti (intrant ingest)" "$(sz "$SRC_CONVERT") · $(find "$SRC_CONVERT" -name '*.html' 2>/dev/null | wc -l) fichiers"
if [ -s "$DATA/sources/.degraded.tsv" ]; then
  row "conversions degradees" "$(wc -l < "$DATA/sources/.degraded.tsv") (voir .degraded.tsv)"
fi

printf '\n\033[1m== SHARDS ==\033[0m\n'
n=$(find "$SHARD_DIR" -name '*.duckdb' 2>/dev/null | wc -l)
row "shards DuckDB" "$n · $(sz "$SHARD_DIR")"
if [ "$n" -gt 0 ]; then
  find "$SHARD_DIR" -name '*.duckdb' 2>/dev/null | sort | while read -r f; do
    printf '    %-22s %s\n' "$(basename "$f")" "$(human "$(stat -c%s "$f")")"
  done | head -40
fi

printf '\n\033[1m== DB FUSIONNEE ==\033[0m\n'
if [ -s "$DB_OUT" ]; then
  row "fichier" "$DB_OUT ($(human "$(stat -c%s "$DB_OUT")"))"
  if [ -x "$RUST_BIN/embed-io" ]; then
    rep="$("$RUST_BIN/embed-io" --db "$DB_OUT" --report 2>/dev/null)"
    if [ -n "$rep" ]; then
      row "modele d'embed" "$(printf '%s' "$rep" | grep -o '"model":"[^"]*"' | cut -d'"' -f4)"
      row "clauses vectorisees" "$(printf '%s' "$rep" | grep -o '"embedded_clauses":[0-9]*' | cut -d: -f2)"
      row "sans vecteur (au floor)" "$(printf '%s' "$rep" | grep -o '"null_embeddings_at_floor":[0-9]*' | cut -d: -f2)"
      row "index HNSW" "$(printf '%s' "$rep" | grep -o '"hnsw":[a-z]*' | cut -d: -f2)"
    fi
  else
    row "(binaires non construits)" "lance 'make corpus' pour les compiler"
  fi
else
  row "fichier" "ABSENT — lance 'make corpus-seed' puis 'make corpus'"
fi

printf '\n\033[1m== DELTA / REPRISE ==\033[0m\n'
if [ -s "$CORPUS_INDEX" ]; then
  row "corpus-index.json" "$(human "$(stat -c%s "$CORPUS_INDEX")") · ancre du delta"
  row "  derniere ecriture" "$(date -r "$CORPUS_INDEX" '+%Y-%m-%d %H:%M' 2>/dev/null)"
else
  row "corpus-index.json" "ABSENT — la prochaine passe sera un FULL"
fi
if [ -s "$VEC_LEDGER" ]; then
  lines=$(wc -l < "$VEC_LEDGER")
  row "ledger de vecteurs" "$(human "$(stat -c%s "$VEC_LEDGER")") · $lines vecteurs"
  [ -f "$VEC_DIR/.identity" ] && row "  identite d'embed" "$(cat "$VEC_DIR/.identity")"
  printf '    \033[2mc%s\033[0m\n' "e ledger porte la dedup inter-release : le relire evite le GPU"
else
  row "ledger de vecteurs" "vide — le prochain embed partira de zero"
fi

printf '\n\033[1m== MACHINE ==\033[0m\n'
row "disque libre" "$(free_gb "$DATA")Go"
row "RAM" "$(awk '/MemTotal/{printf "%.1f Go", $2/1024/1024}' /proc/meminfo 2>/dev/null || echo '?')"
if have_gpu; then
  row "GPU" "$(nvidia-smi --query-gpu=name,memory.total --format=csv,noheader | head -1)"
else
  row "GPU" "non visible — l'embed echouera (--require-cuda)"
fi
printf '\n'
