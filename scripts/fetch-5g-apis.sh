#!/usr/bin/env bash
#
# fetch-5g-apis.sh — download the canonical 3GPP 5GC OpenAPI YAMLs from the
# 3GPP Forge GitLab into the local corpus, pinned to an immutable commit SHA per
# release so a given fetch is reproducible (CLAUDE.md §1 "Reproductibilité").
#
# Layout produced:
#   data/sources/5g-apis/<Rel-NN>/<sha>/<TSxxxxx_Service>.yaml
#   data/sources/5g-apis/<Rel-NN>/manifest.json   {release, ref, sha, files, fetched_at}
#
# "Degrade, don't block" (corpus.sh doctrine): a failed download warns and the
# run continues; the network step is entirely outside the Go binary.
#
#   ./scripts/fetch-5g-apis.sh                 # default: REL-18
#   ./scripts/fetch-5g-apis.sh REL-17 REL-18   # selected releases
#   ./scripts/fetch-5g-apis.sh all             # REL-15 .. REL-20
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(dirname "$SCRIPT_DIR")"
OUT="$ROOT/data/sources/5g-apis"

PROJECT="all%2F5G_APIs"
API="https://forge.3gpp.org/rep/api/v4/projects/$PROJECT/repository"
RAW="https://forge.3gpp.org/rep/all/5G_APIs/-/raw"
UA="Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

log()  { printf '[5g-apis] %s\n' "$*"; }
warn() { printf '[5g-apis][warn] %s\n' "$*" >&2; }

# releases to fetch
releases=("$@")
if [[ ${#releases[@]} -eq 0 ]]; then releases=("REL-18"); fi
if [[ "${releases[0]}" == "all" ]]; then
  releases=(REL-15 REL-16 REL-17 REL-18 REL-19 REL-20)
fi

# resolve_sha REF -> prints the head commit SHA of REF, or empty on failure.
resolve_sha() {
  local ref="$1"
  curl -fsS -A "$UA" --max-time 30 "$API/branches/$ref" 2>/dev/null \
    | python3 -c 'import sys,json;print(json.load(sys.stdin)["commit"]["id"])' 2>/dev/null
}

# list_yaml REF -> prints all *.yaml blob names at REF (paged, 100/page).
list_yaml() {
  local ref="$1" page=1 names
  while :; do
    names=$(curl -fsS -A "$UA" --max-time 60 "$API/tree?ref=$ref&per_page=100&page=$page" 2>/dev/null \
      | python3 -c 'import sys,json
d=json.load(sys.stdin)
[print(x["name"]) for x in d if x["type"]=="blob" and x["name"].endswith(".yaml")]' 2>/dev/null)
    [[ -z "$names" ]] && break
    printf '%s\n' "$names"
    (( page++ ))
    (( page > 30 )) && break   # safety bound (~3000 files)
  done
}

total_files=0
for ref in "${releases[@]}"; do
  rel="${ref/REL-/Rel-}"          # REL-18 -> Rel-18 (matches corpus layout)
  log "resolving $ref ..."
  sha="$(resolve_sha "$ref")"
  if [[ -z "$sha" ]]; then warn "$ref: cannot resolve SHA (skip)"; continue; fi
  dest="$OUT/$rel/$sha"
  mkdir -p "$dest"
  log "$ref -> $sha"

  mapfile -t files < <(list_yaml "$ref")
  if [[ ${#files[@]} -eq 0 ]]; then warn "$ref: no YAML files listed (skip)"; continue; fi

  n=0
  for f in "${files[@]}"; do
    out="$dest/$f"
    if [[ -s "$out" ]]; then (( n++ )); continue; fi    # incremental
    if curl -fsS -A "$UA" --max-time 60 -o "$out" "$RAW/$sha/$f" 2>/dev/null; then
      (( n++ ))
    else
      warn "$ref: download failed for $f"
      rm -f "$out"
    fi
  done

  # manifest (reproducibility record)
  python3 - "$dest/../manifest.json" "$rel" "$ref" "$sha" "${files[@]}" <<'PY'
import json,sys,datetime
out,rel,ref,sha=sys.argv[1:5]; files=sys.argv[5:]
json.dump({"release":rel,"ref":ref,"sha":sha,"files":sorted(files),
          "fetched_at":datetime.datetime.now(datetime.timezone.utc).isoformat()},
         open(out,"w"),indent=2)
PY
  log "$rel: $n/${#files[@]} YAML files in $dest"
  (( total_files += n ))
done

log "done — $total_files YAML files under $OUT"
