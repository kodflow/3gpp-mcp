#!/usr/bin/env bash
#
# fetch-li-asn.sh — bring the TS 33.128 ASN.1 payload registry into the local
# corpus so `enrich` can fill li_events/asn1_types instead of logging
# "no TS33128Payloads .asn found — li_events stays empty".
#
# The modules are not published on their own: they ride inside a zip inside the
# zip of the spec (docs/research/axes/01-li-asn1-events.md):
#
#   archive/33_series/33.128/33128-<code>.zip
#     └─ 33128-<code>-attachments.zip
#          ├─ TS33128Payloads.asn            <- the event registry
#          ├─ TS33128IdentityAssociation.asn
#          └─ TS33128Dictionaries.xml
#
# Layout produced:
#   data/sources/asn/<Rel-NN>/TS33128*.asn|.xml
#
# The version code is READ FROM THE CORPUS (data/sources/convert/<Rel>/33128-*.html),
# never guessed: the registry must describe the same version whose text we
# ingested, otherwise li_events would document a spec the corpus does not hold.
# The origin zips are purged after conversion on this machine, hence the refetch.
#
#   ./scripts/fetch-li-asn.sh              # every release the corpus knows
#   ./scripts/fetch-li-asn.sh Rel-19       # selected releases
#
# "Degrade, don't block" (corpus.sh doctrine): a release whose zip carries no
# attachments warns and the run continues — Rel-15 and some Rel-19 builds
# genuinely ship without them.
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(dirname "$SCRIPT_DIR")"
CONVERT="$ROOT/data/sources/convert"
OUT="$ROOT/data/sources/asn"
BASE="https://www.3gpp.org/ftp/Specs/archive/33_series/33.128"
UA="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

log()  { printf '[li-asn] %s\n' "$*"; }
warn() { printf '[li-asn][warn] %s\n' "$*" >&2; }

command -v unzip >/dev/null 2>&1 || { warn "unzip is not on PATH — nothing can be extracted"; exit 1; }

# releases to serve: the argument list, or every Rel-* the corpus converted
releases=("$@")
if [[ ${#releases[@]} -eq 0 ]]; then
  releases=()
  for d in "$CONVERT"/Rel-*; do
    [[ -d "$d" ]] || continue
    releases+=("$(basename "$d")")
  done
fi

got=0
for rel in "${releases[@]}"; do
  # the code of the 33.128 version this corpus actually holds
  html=""
  for f in "$CONVERT/$rel"/33128-*.html; do
    [[ -f "$f" ]] && html="$f"
  done
  if [[ -z "$html" ]]; then
    log "$rel: no 33.128 in the corpus — skipping"
    continue
  fi
  code="$(basename "$html")"; code="${code#33128-}"; code="${code%.html}"

  dest="$OUT/$rel"
  if [[ -f "$dest/TS33128Payloads.asn" ]]; then
    log "$rel: already have TS33128Payloads.asn (33128-$code)"
    got=$((got + 1))
    continue
  fi

  tmp="$(mktemp -d 2>/dev/null || echo "${TMPDIR:-/tmp}/li-asn-$$-$rel")"
  mkdir -p "$tmp" || { warn "$rel: no scratch directory"; continue; }
  zip="$tmp/33128-$code.zip"

  log "$rel: fetching 33128-$code.zip"
  if ! curl -fsS -A "$UA" --max-time 300 -o "$zip" "$BASE/33128-$code.zip"; then
    warn "$rel: 33128-$code.zip is not downloadable (403 on www.3gpp.org means not found)"
    rm -rf "$tmp"; continue
  fi
  if ! unzip -qo "$zip" -d "$tmp/outer" 2>/dev/null; then
    warn "$rel: 33128-$code.zip does not unzip"
    rm -rf "$tmp"; continue
  fi

  # the attachments are a SECOND zip inside the first one
  inner=""
  for f in "$tmp/outer"/*attachments*.zip "$tmp/outer"/*/*attachments*.zip; do
    [[ -f "$f" ]] && inner="$f"
  done
  if [[ -z "$inner" ]]; then
    warn "$rel: 33128-$code.zip carries no *-attachments.zip (this happens; Rel-15 has none)"
    rm -rf "$tmp"; continue
  fi
  unzip -qo "$inner" -d "$tmp/inner" 2>/dev/null || true

  mkdir -p "$dest"
  n=0
  while IFS= read -r f; do
    [[ -f "$f" ]] || continue
    cp -f "$f" "$dest/$(basename "$f")" && n=$((n + 1))
  done < <(find "$tmp/inner" -type f \( -name 'TS33128*.asn' -o -name 'TS33128*.xml' \) 2>/dev/null)

  if [[ "$n" -eq 0 ]]; then
    warn "$rel: the attachments zip holds no TS33128* module"
    rmdir "$dest" 2>/dev/null
  else
    log "$rel: $n module(s) from 33128-$code -> data/sources/asn/$rel"
    got=$((got + 1))
  fi
  rm -rf "$tmp"
done

if [[ "$got" -eq 0 ]]; then
  warn "no ASN.1 registry acquired — li_events will stay empty"
  exit 1
fi
log "done: $got release(s) carry a registry under data/sources/asn"
