#!/usr/bin/env bash
#
# verify-coverage.sh — classify EVERY worklist URL the way the real build resolves
# it, so "success" reflects download_zip (exact URL + same-release archive-highest
# fallback), not just the exact URL. Range-GET only (first byte) → bandwidth-light.
#
#   discover --emit-worklist --floor Rel-99 > wl.txt
#   scripts/verify-coverage.sh wl.txt [out_dir]
#
# Per line "<release> <url> <name>" emits one of:
#   ok-direct    the exact version downloads
#   ok-fallback  exact missing, but the same-release archive-highest exists
#   absent       no same-release version in the archive (genuine 3GPP gap → ledger)
# out_dir/absent.tsv is the known-absent ledger (release<TAB>spec).
set -uo pipefail

WL="${1:?usage: verify-coverage.sh <worklist.txt> [out_dir]}"
OUT="${2:-/tmp/coverage}"
JOBS="${JOBS:-8}"
UA="Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"
mkdir -p "$OUT"; : > "$OUT/results.tsv"

decode_char() { local c="$1"; case "$c" in [0-9]) printf '%d' "$c";; [a-z]) printf '%d' "$(( 10 + $(printf '%d' "'$c") - 97 ))";; *) printf '%d' 999;; esac; }

http() { curl -s -o /dev/null -A "$UA" --connect-timeout 15 --max-time 60 -r 0-0 -w '%{http_code}' "$1" 2>/dev/null || echo 000; }
http_retry() { local u="$1" c t=0; while :; do c=$(http "$u"); case "$c" in 200|206) echo "$c"; return;; 000|429|5*) t=$((t+1)); [ $t -ge 5 ] && { echo "$c"; return; }; sleep $((t*2));; *) echo "$c"; return;; esac; done; }

verify_one() {  # <release> <url> <name>
  local rel="$1" url="$2" name="$3" code dir prefix relmaj html f c3 m best=""
  code=$(http_retry "$url")
  if [[ "$code" == 200 || "$code" == 206 ]]; then echo -e "ok-direct\t$rel\t$url"; return; fi
  case "$rel" in Rel-99) relmaj=3;; Rel-*) relmaj="${rel#Rel-}";; *) relmaj=0;; esac
  dir="${url%/*}/"; prefix="${name%.zip}"; prefix="${prefix%-*}"
  html="$(curl -s -A "$UA" --connect-timeout 15 --max-time 90 "$dir" 2>/dev/null || true)"
  if [[ -n "$html" ]]; then
    while IFS= read -r f; do
      [[ -z "$f" ]] && continue
      c3="${f##*-}"; c3="${c3%.zip}"
      if [[ "$c3" =~ ^[0-9]{6}$ ]]; then m=$((10#${c3:0:2})); else m="$(decode_char "${c3:0:1}")"; fi
      (( m == 999 )) && continue
      if (( relmaj > 0 )); then (( m == relmaj )) && best="$f"; else best="$f"; fi
    done < <(printf '%s' "$html" | grep -oE "${prefix}-([0-9a-z]{3}|[0-9]{6})\.zip" | sort -u)
  fi
  if [[ -n "$best" ]]; then echo -e "ok-fallback\t$rel\t$url"; else echo -e "absent\t$rel\t$url"; fi
}
export -f decode_char http http_retry verify_one
export UA

# worklist lines are space-separated "rel url name" → pass as 3 args (like corpus.sh).
xargs -P "$JOBS" -n 3 bash -c 'verify_one "$0" "$1" "$2"' < "$WL" >> "$OUT/results.tsv"

echo "=== coverage ($(wc -l < "$OUT/results.tsv") of $(wc -l < "$WL")) ==="
cut -f1 "$OUT/results.tsv" | sort | uniq -c | sort -rn | tee "$OUT/summary"
awk -F'\t' '$1=="absent"{print $2"\t"$3}' "$OUT/results.tsv" | sed -E 's#.*/([0-9]{2}\.[0-9]{3}(-[0-9]+)?)/.*#\1#' > "$OUT/absent.specs" || true
awk -F'\t' '$1=="absent"{print $0}' "$OUT/results.tsv" > "$OUT/absent.tsv"
echo "absent (genuine 3GPP gaps, ledger candidates): $(wc -l < "$OUT/absent.tsv")"
