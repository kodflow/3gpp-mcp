#!/usr/bin/env bash
#
# probe-worklist.sh — verify that every URL in a corpus fetch worklist is really
# downloadable (no silent 403/404), with aggressive retry + backoff so a transient
# rate-limit is distinguished from a genuinely-absent file. Downloads only the
# FIRST BYTE (range 0-0) — bandwidth-light, exercises the real GET path.
#
#   discover --emit-worklist --floor Rel-99 > wl.txt
#   scripts/probe-worklist.sh wl.txt [out_dir]
#
# Output (out_dir, default /tmp/probe):
#   results.tsv   "<http_code>\t<url>"   (final code after retries)
#   ok.txt        URLs that returned 200/206
#   fail.txt      URLs still failing after retries  (the genuine "absent" ledger)
#   summary       code histogram
set -uo pipefail

WL="${1:?usage: probe-worklist.sh <worklist.txt> [out_dir]}"
OUT="${2:-/tmp/probe}"
JOBS="${JOBS:-8}"            # concurrency (modest, to not trip the 3GPP rate-limit)
MAX_TRIES="${MAX_TRIES:-6}"  # plenty of retries — the user wants certainty
UA="Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"
mkdir -p "$OUT"
: > "$OUT/results.tsv"

probe_one() {
  local url="$1" code t=0 delay
  while :; do
    code=$(curl -s -o /dev/null -A "$UA" --connect-timeout 15 --max-time 60 -r 0-0 \
             -w '%{http_code}' "$url" 2>/dev/null || echo 000)
    case "$code" in
      200|206) break ;;                              # got it
      403|429|5*|000)                                # maybe transient (rate-limit / hiccup)
        t=$((t+1)); [ "$t" -ge "$MAX_TRIES" ] && break
        delay=$(( t*t*2 )); [ "$delay" -gt 30 ] && delay=30   # 2,8,18,30,30s backoff
        sleep "$delay" ;;
      *) break ;;                                    # 404 etc. → terminal
    esac
  done
  printf '%s\t%s\n' "$code" "$url"
}
export -f probe_one
export UA MAX_TRIES

awk '{print $2}' "$WL" | xargs -P "$JOBS" -I{} bash -c 'probe_one "$@"' _ {} >> "$OUT/results.tsv"

awk -F'\t' '$1==200||$1==206{print $2}' "$OUT/results.tsv" > "$OUT/ok.txt"
awk -F'\t' '$1!=200&&$1!=206{print $2}' "$OUT/results.tsv" > "$OUT/fail.txt"
echo "=== code histogram ($(wc -l < "$OUT/results.tsv") urls, $JOBS jobs, up to $MAX_TRIES tries) ==="
cut -f1 "$OUT/results.tsv" | sort | uniq -c | sort -rn | tee "$OUT/summary"
echo "ok=$(wc -l < "$OUT/ok.txt")  fail=$(wc -l < "$OUT/fail.txt")  (fail list: $OUT/fail.txt)"
