#!/usr/bin/env bash
#
# recover-fails.sh — one-off re-pass over specs that failed conversion in the
# main run (FAILCV lines in data/sources/.run.log), with a long timeout. Uses
# the same convert_doc as corpus.sh, so genuinely-slow mega-specs get more time
# AND soffice crashes get the EMF/WMF-strip retry (tagged degraded). Idempotent:
# targets that already exist are skipped.
#
# Usage: [CONV_TIMEOUT=900] [JOBS=2] scripts/recover-fails.sh
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ORIGIN="$ROOT/data/sources/origin"
CONVERT="$ROOT/data/sources/convert"
LOG="$ROOT/data/sources/.run.log"
JOBS="${JOBS:-2}"

# shellcheck source=lib/convert.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib/convert.sh"
export CONV_TIMEOUT CONV_KILL DEGRADED_TSV CONVERT
export -f convert_doc _soffice_html

mapfile -t ZIPS < <(grep FAILCV "$LOG" 2>/dev/null | grep -v '\._' \
  | grep -oE "$ORIGIN/Rel-[0-9]+/[0-9]+-[0-9a-z]+\.zip" | sort -u)
echo "$(date -Is) [recover] ${#ZIPS[@]} failed zips to retry (timeout=${CONV_TIMEOUT}s, jobs=$JOBS)"
[[ ${#ZIPS[@]} -eq 0 ]] && { echo "$(date -Is) [recover] nothing to do"; exit 0; }

recover_one() {
  local zip="$1" rel base tmp inner target t0
  rel="$(basename "$(dirname "$zip")")"
  tmp="$(mktemp -d)"
  if unzip -qo "$zip" -d "$tmp" 2>/dev/null; then
    while IFS= read -r inner; do
      base="$(basename "$inner")"; base="${base%.*}"
      target="$CONVERT/$rel/$base.html"
      [[ -s "$target" ]] && continue
      t0=$SECONDS
      if convert_doc "$inner" "$target" "$rel/$base"; then
        echo "$(date -Is) ${CONV_STATUS^^} $rel/$base.html ($((SECONDS - t0))s)"
      else
        echo "$(date -Is) STILLFAIL $rel/$base ($((SECONDS - t0))s)"
      fi
    done < <(find "$tmp" -type f \( -iname '*.doc' -o -iname '*.docx' \) -not -name '._*' -not -name '~$*')
  fi
  chmod -R u+w "$tmp" 2>/dev/null || true
  rm -rf "$tmp" 2>/dev/null || true
}
export -f recover_one; export ORIGIN CONVERT

printf '%s\n' "${ZIPS[@]}" | xargs -P "$JOBS" -I{} bash -c 'recover_one "$0"' {}
echo "$(date -Is) [recover] done"
