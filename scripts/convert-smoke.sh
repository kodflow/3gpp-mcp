#!/usr/bin/env bash
#
# convert-smoke.sh — regression test for the convert fallback chain on the KNOWN
# HARD specs: a soffice EMF/.doc crasher (33.501), a huge multi-part conformance
# test spec (36.521-1), and a legacy .doc (24.008). Each MUST come out clean or
# degraded — never FAILCV. Proves the lib/convert.sh chain (soffice → EMF-strip →
# pandoc → antiword/catdoc text-salvage) recovers the documents that used to be
# lost. Needs the convert toolchain: libreoffice-writer-nogui pandoc antiword catdoc.
#
#   scripts/convert-smoke.sh        # exits non-zero if any spec FAILCV
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UA="Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"
WORK="${WORK:-$(mktemp -d)}"
export CONV_TIMEOUT="${CONV_TIMEOUT:-600}" CONV_KILL=30 DEGRADED_TSV="$WORK/.degraded.tsv"
# shellcheck source=lib/convert.sh
source "$ROOT/scripts/lib/convert.sh"

command -v soffice >/dev/null || { echo "convert-smoke: soffice missing (apt: libreoffice-writer-nogui)"; exit 2; }

# spec -> archive path of a real published version.
cases=(
  "33.501|33_series/33.501/33501-j10.zip"      # documented soffice crasher → must salvage
  "36.521-1|36_series/36.521-1/36521-1-i00.zip" # 27MB multi-part conformance test
  "24.008|24_series/24.008/24008-450.zip"       # legacy .doc
)
rc=0
for c in "${cases[@]}"; do
  spec="${c%%|*}"; path="${c#*|}"
  zip="$WORK/$spec.zip"
  if ! curl -fsSL -A "$UA" --retry 3 --max-time 300 -o "$zip" "https://www.3gpp.org/ftp/Specs/archive/$path"; then
    echo "$spec: SKIP (download failed — archive may have rolled the version)"; continue
  fi
  d="$WORK/x_$spec"; rm -rf "$d"; mkdir -p "$d"; unzip -qo "$zip" -d "$d" 2>/dev/null
  doc=$(find "$d" -type f \( -iname '*.doc' -o -iname '*.docx' \) -not -name '._*' -not -name '~$*' | head -1)
  [ -z "$doc" ] && { echo "$spec: FAIL (no doc/docx in zip)"; rc=1; continue; }
  if convert_doc "$doc" "$WORK/$spec.html" "$spec"; then
    echo "$spec: OK ($CONV_STATUS, $(wc -c < "$WORK/$spec.html") bytes)"
  else
    echo "$spec: FAILCV — convert chain did not recover it"; rc=1
  fi
done
[ "$rc" -eq 0 ] && echo "convert-smoke: PASS (every hard spec clean or degraded)" || echo "convert-smoke: FAIL"
exit "$rc"
