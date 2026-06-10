#!/usr/bin/env bash
#
# build-absent-index.sh — turn verify-coverage.sh's unfetchable rows into the
# absent-index.json ledger that `discover --absent-index` consumes: a JSON object
# "<spec_id>|<Rel-NN>" -> "<X.Y.Z>" recording, at the version the STATUS REPORT
# claims, every (spec,release) whose CLAIMED-latest file is not downloadable.
#
#   scripts/build-absent-index.sh /tmp/coverage/results.tsv data/absent-index.json
#
# Two row classes are ledgered — both are 3GPP "phantom" claimed versions:
#   - absent      : no same-release file at all (genuine gap).
#   - ok-fallback : the EXACT claimed-latest version is 403/unavailable, only an
#                   OLDER same-release version exists. corpus.sh indexes that older
#                   best-available version (same fallback as coverage), so the index
#                   sits one bump below the phantom the status report advertises.
# Without ledgering ok-fallback, discover sees indexed < claimed and re-selects the
# series on EVERY run forever (the delta never converges to 0). Recording the claimed
# version makes discover account it — and a genuinely-newer DOWNLOADABLE version later
# classifies ok-direct (not ledgered) so it is correctly re-flagged. The version is
# decoded from the archive file code (3-char base36, or the 6-digit decimal form for
# high components) so it matches the status-report version discover compares against.
set -uo pipefail

RES="${1:?usage: build-absent-index.sh <results.tsv> <out.json>}"
OUT="${2:?usage: build-absent-index.sh <results.tsv> <out.json>}"

decode_char() { local c="$1"; case "$c" in [0-9]) printf '%d' "$c";; [a-z]) printf '%d' "$(( 10 + $(printf '%d' "'$c") - 97 ))";; *) printf '%d' 0;; esac; }

ver_from_code() {  # <code> -> X.Y.Z
  local c="$1"
  if [[ "$c" =~ ^[0-9]{6}$ ]]; then
    printf '%d.%d.%d' "$((10#${c:0:2}))" "$((10#${c:2:2}))" "$((10#${c:4:2}))"
  else
    printf '%d.%d.%d' "$(decode_char "${c:0:1}")" "$(decode_char "${c:1:1}")" "$(decode_char "${c:2:1}")"
  fi
}

tmp="$(mktemp)"
printf '{\n' > "$tmp"
first=1
# rows: "<state><TAB><rel><TAB><url>" — ledger the phantom-claimed classes only.
while IFS=$'\t' read -r state rel url; do
  case "$state" in absent | ok-fallback) ;; *) continue ;; esac
  spec="$(printf '%s' "$url" | grep -oE '[0-9]{2}\.[0-9]{3}(-[0-9]+)?' | head -1)"
  name="${url##*/}"; code="${name%.zip}"; code="${code##*-}"
  ver="$(ver_from_code "$code")"
  [[ -n "$spec" && -n "$rel" ]] || continue
  [[ $first -eq 1 ]] && first=0 || printf ',\n' >> "$tmp"
  printf '  "%s|%s": "%s"' "$spec" "$rel" "$ver" >> "$tmp"
done < "$RES"
printf '\n}\n' >> "$tmp"
mv -f "$tmp" "$OUT"
n=$(grep -c '": "' "$OUT" || true)
echo "absent-index: $n entries -> $OUT"
