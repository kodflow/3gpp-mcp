#!/usr/bin/env bash
#
# convert-origin.sh — convert EVERY spec ZIP already mirrored under
# data/sources/origin/<Rel>/ to HTML in data/sources/convert/<Rel>/, with no
# network access (download already done). Idempotent: existing HTML is skipped,
# so this only fills the gaps (e.g. the legacy releases Rel-4..16 that the
# SET=Rel-17 floor never converted). Reuses convert_doc (EMF-strip retry +
# degraded tagging). Run: [JOBS=4] scripts/convert-origin.sh [Rel-globs...]
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ORIGIN="$ROOT/data/sources/origin"
CONVERT="$ROOT/data/sources/convert"
JOBS="${JOBS:-4}"

# shellcheck source=lib/convert.sh
source "$ROOT/scripts/lib/convert.sh"
export CONV_TIMEOUT CONV_KILL DEGRADED_TSV CONVERT
convert_export_fns

command -v soffice >/dev/null 2>&1 || {
  echo "$(date -Is) soffice missing — installing libreoffice-writer ..." >&2
  sudo DEBIAN_FRONTEND=noninteractive apt-get update -qq && \
    sudo DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends libreoffice-writer >/dev/null 2>&1 || true
  command -v soffice >/dev/null 2>&1 || { echo "ERROR: soffice unavailable" >&2; exit 1; }
}

conv_zip() {
  local zip="$1" rel base tmp inner target
  rel="$(basename "$(dirname "$zip")")"
  tmp="$(mktemp -d)"
  if unzip -qo "$zip" -d "$tmp" 2>/dev/null; then
    while IFS= read -r inner; do
      base="$(basename "$inner")"; base="${base%.*}"
      target="$CONVERT/$rel/$base.html"
      [[ -s "$target" ]] && continue
      convert_doc "$inner" "$target" "$rel/$base" \
        || echo "$(date -Is) FAILCV $zip :: $(basename "$inner")" >&2
    done < <(find "$tmp" -type f \( -iname '*.doc' -o -iname '*.docx' \) -not -name '._*' -not -name '~$*')
  fi
  rm -rf "$tmp"
}
export -f conv_zip
export ORIGIN CONVERT

# Heartbeat: prove liveness + surface stalls.
( while sleep 180; do echo "$(date -Is) [convert-origin] $(find "$CONVERT" -name '*.html' | wc -l) html so far" >&2; done ) &
HB=$!
trap 'kill "$HB" 2>/dev/null || true' EXIT

globs=("$@")
[[ ${#globs[@]} -eq 0 ]] && globs=("*")
for g in "${globs[@]}"; do
  find "$ORIGIN"/$g -name '*.zip' 2>/dev/null
done | sort -u | xargs -P "$JOBS" -I{} bash -c 'conv_zip "$0"' {}

echo "$(date -Is) [convert-origin] done — convert: $(find "$CONVERT" -name '*.html' | wc -l) html ($(du -sh "$CONVERT" 2>/dev/null | cut -f1))"
