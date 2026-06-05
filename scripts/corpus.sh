#!/usr/bin/env bash
#
# corpus.sh — single entry point that builds the local 3GPP corpus for the MCP.
#
# Everything is done ON THE FLY: one worker per spec downloads -> unzips ->
# converts to HTML in a single streaming flow (no separate batch phases). The
# whole run is incremental (only what is missing is fetched/converted) and
# guarded by flock (overlapping cron invocations are safe).
#
#   per spec:  download ZIP -> data/sources/origin/<Release>/<spec>.zip
#              unzip + convert each doc -> data/sources/convert/<Release>/<spec>.html
#
# RELEASE FLOOR — variable SET (default "Rel-17"):
#   Only releases whose MAJOR version >= SET are processed, newest-first in
#   priority. In 3GPP's encoding a release's major IS its ordinal: Rel-17=17,
#   Rel-20=20 ... and Rel-99 = major 3 (it is Release *1999*, not "99"). So
#   SET=Rel-17 yields Rel-17/18/19/20 and NEVER Rel-99. Lower SET later
#   (e.g. SET=Rel-4) to ingest older releases; Rel-99 / Phase* stay out.
#   Override: SET=Rel-15 scripts/corpus.sh   or   scripts/corpus.sh --set Rel-15
#
# Source of truth = the 3GPP STATUS REPORT (via cmd/discover --emit-worklist):
# EVERY (spec,release) it lists at/above the floor is fetched — drafts (v0/v1/v2)
# INCLUDED, so the index matches the site and nothing is silently dropped. Legacy
# GSM (4-digit / series 00-13) is omitted by the report → archive enumeration,
# opt-in via LEGACY_GSM=1.
# HTML comes from LibreOffice headless — the only tool reading the ~55% legacy
# binary .doc while keeping structure (headings = clauses, tables). The query
# MCP binary stays pure-Go; LibreOffice is used only here, offline.
#
# Crontab (daily 03:17, newest releases):
#   17 3 * * *  /workspace/scripts/corpus.sh >> /workspace/data/sources/.cron.log 2>&1
#
# Flags: --set Rel-N  --jobs N  --enum-jobs N  --series "23 33"
#        --no-download  --no-convert  --quick  --help
#
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(dirname "$SCRIPT_DIR")"

BASE="https://www.3gpp.org/ftp/Specs/archive"
UA="Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"
ORIGIN="$ROOT/data/sources/origin"
CONVERT="$ROOT/data/sources/convert"
DONE="$ROOT/data/sources/.corpus.done"

# Shared DOC/DOCX -> HTML conversion: straight export, then EMF/WMF-strip retry
# on a soffice crash, with degraded results tagged in-file + in .degraded.tsv.
# shellcheck source=lib/convert.sh
source "$SCRIPT_DIR/lib/convert.sh"
CONV_TIMEOUT="${CONV_TIMEOUT:-900}"   # was a hard 240s — big specs (28552/33501) need ~700s

SET="${SET:-Rel-99}"          # release floor (env-overridable). Rel-99 = every real
                              # 3GPP release (Rel-99 + Rel-4..latest). Floor is on the
                              # RELEASE (per the status report), NOT the version-major,
                              # so a draft v1.x of an in-scope release is kept. Future
                              # releases auto.
JOBS=4                        # per-spec workers (soffice is RAM-heavy)
ENUM_JOBS=8                   # enumeration workers (network-bound)
SERIES_FILTER=""
DO_DOWNLOAD=1
DO_CONVERT=1
QUICK=0
MIN_FREE_GB=5

while [[ $# -gt 0 ]]; do
  case "$1" in
    --set)         SET="$2"; shift 2;;
    --jobs)        JOBS="$2"; shift 2;;
    --enum-jobs)   ENUM_JOBS="$2"; shift 2;;
    --series)      SERIES_FILTER="$2"; shift 2;;
    --no-download) DO_DOWNLOAD=0; shift;;
    --no-convert)  DO_CONVERT=0; shift;;
    --quick)       QUICK=1; shift;;
    -h|--help)     sed -n '2,46p' "$0"; exit 0;;
    *) echo "unknown arg: $1" >&2; exit 2;;
  esac
done

SET_MAJOR="$(printf '%s' "$SET" | grep -oE '[0-9]+' | head -1)"; SET_MAJOR="${SET_MAJOR:-3}"
[[ "$SET" == "Rel-99" ]] && SET_MAJOR=3   # Rel-99 == version major 3 (not 99)

# Whole MODERN corpus (Rel-99 + Rel-4..latest) is the DEFAULT (SET=Rel-99 above).
#
# GSM Phase 1/2 (4-digit specs / legacy series 00-13) is OFF by default. Its filenames
# use a reverse-engineered version-code scheme that CANNOT be decoded to an exact 3GPP
# release line, so guessing a release would violate the cite-exactly rule (CLAUDE.md §1).
# By an explicit architectural decision (§0), INCLUDE_LEGACY_GSM=1 ingests them
# LEXICALLY with the single honest release bucket "GSM" (the Go ingest stamps it;
# see model.ReleaseGSM): spec_id, clause, the literal code version and url stay exact,
# and because ReleaseOrdinal("GSM") is false these clauses are below every embed floor
# → indexed for BM25/LIKE search but NEVER vectorised. Vectors stay Rel-99..latest.
LEGACY_GSM="${INCLUDE_LEGACY_GSM:-0}"

mkdir -p "$ORIGIN" "$CONVERT"

LOCK="$ROOT/data/sources/.corpus.lock"
exec 9>"$LOCK"
if ! flock -n 9; then echo "$(date -Is) [corpus] another run in progress — exiting" >&2; exit 0; fi

log() { echo "$(date -Is) [corpus] $*" >&2; }
rm -f "$DONE"

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fetch() { curl -fsSL -A "$UA" --retry 3 --retry-delay 2 --connect-timeout 20 --max-time 900 "$@"; }
export -f fetch; export UA

decode_char() {
  local c="$1"
  case "$c" in
    [0-9]) printf '%d' "$c";;
    [a-z]) printf '%d' "$(( 10 + $(printf '%d' "'$c") - 97 ))";;
    *)     printf '%d' 999;;
  esac
}
export -f decode_char

# emit "<release> <url> <name>" for the highest version of each release >= SET_MAJOR
emit_spec() {
  local s="$1" spec="$2" num dir html files legacy=0
  num="${spec/./}"
  if [[ ${#num} -le 4 ]]; then                     # 4-digit = legacy GSM Phase 1/2
    [[ "${LEGACY_GSM:-0}" == "1" ]] || return 0    # only when explicitly opted in (§0)
    legacy=1
  fi
  dir="$BASE/${s}_series/${spec}/"
  html="$(fetch "$dir" 2>/dev/null || true)"; [[ -z "$html" ]] && return 0
  files="$(printf '%s' "$html" | grep -oE '[0-9]{4,5}-[0-9a-z]{3}\.zip' | sort -u || true)"
  [[ -z "$files" ]] && return 0
  declare -A best
  local f code c1 c2 c3 m v2 v3 key cur
  while IFS= read -r f; do
    [[ -z "$f" ]] && continue
    code="${f##*-}"; code="${code%.zip}"
    c1="${code:0:1}"; c2="${code:1:1}"; c3="${code:2:1}"
    m="$(decode_char "$c1")"
    (( m == 999 )) && continue
    (( legacy == 0 && m < SET_MAJOR )) && continue # release floor (modern only; legacy keeps all)
    v2="$(decode_char "$c2")"; v3="$(decode_char "$c3")"
    (( v2 == 999 )) && v2=0; (( v3 == 999 )) && v3=0
    key=$(( m * 10000 + v2 * 100 + v3 ))
    cur="${best[$m]:-}"
    if [[ -z "$cur" ]] || (( key > ${cur%%:*} )); then best[$m]="${key}:${f}"; fi
  done <<< "$files"
  # rel here is only the manifest hint; the Go ingest is the source of truth and
  # stamps "GSM" for 4-digit specs (model.ReleaseGSM) regardless.
  local out="$WORKDIR/${s}.${spec}.lines" rel
  for m in "${!best[@]}"; do
    f="${best[$m]##*:}"
    if (( legacy == 1 )); then rel="GSM"; elif (( m == 3 )); then rel="Rel-99"; else rel="Rel-$m"; fi
    printf '%s %s %s\n' "$rel" "${dir}${f}" "$f" >> "$out"
  done
}
export -f emit_spec; export BASE WORKDIR SET_MAJOR LEGACY_GSM

# download_zip <rel> <url> <name> — robustly fetch one spec zip. Prints the local
# path on success (rc 0); rc 1 + a FAILDL log on genuine absence. Strategy:
#   1. the worklist URL, with AGGRESSIVE retry: transient (000/429/5xx) up to 6×
#      with backoff; 403/404 twice (a momentary edge miss) — measured ~0.8% of
#      URLs are genuinely absent, so we don't burn long backoff on them.
#   2. FALLBACK (the auto-fix): if the exact version is absent, list the spec's
#      archive dir and take the HIGHEST version actually present at/above the floor
#      — recovers "status report ahead of the archive" cases. The release stays the
#      worklist's (correct attribution). Dir 403/empty ⇒ genuine absent ⇒ FAILDL.
download_zip() {
  local rel="$1" url="$2" name="$3" zip="$ORIGIN/$1/$3" t=0 j code dir prefix html best bestkey f c3 m v2 v3 key
  [[ -s "$zip" ]] && { printf '%s\n' "$zip"; return 0; }
  mkdir -p "$ORIGIN/$rel"
  while :; do
    code=$(curl -s -A "$UA" --connect-timeout 20 --max-time 1800 -o "$zip.part" -w '%{http_code}' "$url" 2>/dev/null || echo 000)
    if [[ ( "$code" == 200 || "$code" == 206 ) && -s "$zip.part" ]]; then mv -f "$zip.part" "$zip"; printf '%s\n' "$zip"; return 0; fi
    rm -f "$zip.part"; t=$((t+1))
    # Jitter (0-2s, per-worker via $RANDOM) de-synchronises the parallel workers so
    # a retry storm doesn't re-hammer the 3GPP host on the same beat — a frequent
    # cause of spurious 403/429 on this archive.
    j=$(( RANDOM % 3 ))
    case "$code" in
      000|429|5*) [[ $t -ge 6 ]] && break; sleep $(( (t*3 < 30 ? t*3 : 30) + j ));;  # transient: long backoff
      403)        [[ $t -ge 5 ]] && break; sleep $(( (t*t < 20 ? t*t : 20) + j ));;  # 403 is often transient WAF/rate here: quadratic backoff + jitter, MORE tries before giving up
      404)        [[ $t -ge 2 ]] && break; sleep 1;;                                 # 404 = genuine absent: don't hammer
      *) break;;
    esac
  done
  # FALLBACK — highest version of the SAME RELEASE actually present in the spec's
  # archive dir. Matching the release-major (not just the floor) keeps attribution
  # correct: a Rel-6 miss falls back to the highest Rel-6 file (…-6xx), NEVER a
  # higher release's version that would then be mis-filed under Rel-6. In 3GPP's
  # encoding the version-major IS the release ordinal (Rel-99 = major 3).
  dir="${url%/*}/"; prefix="${name%.zip}"; prefix="${prefix%-*}"
  case "$rel" in
    Rel-99) relmaj=3 ;;
    Rel-*)  relmaj="${rel#Rel-}" ;;
    *)      relmaj=0 ;;   # GSM/legacy: version-major is not the release → take highest
  esac
  html="$(fetch "$dir" 2>/dev/null || true)"
  if [[ -n "$html" ]]; then
    bestkey=-1; best=""
    while IFS= read -r f; do
      [[ -z "$f" ]] && continue
      c3="${f##*-}"; c3="${c3%.zip}"
      # Two archive code forms: 3-char base36 (one char/component, 0..35) and the
      # 6-digit decimal form for high components (08 37 00 = 8.37.0). Decode both.
      if [[ "$c3" =~ ^[0-9]{6}$ ]]; then
        m=$((10#${c3:0:2})); v2=$((10#${c3:2:2})); v3=$((10#${c3:4:2}))
      else
        m="$(decode_char "${c3:0:1}")"; (( m == 999 )) && continue
        v2="$(decode_char "${c3:1:1}")"; v3="$(decode_char "${c3:2:1}")"
        (( v2 == 999 )) && v2=0; (( v3 == 999 )) && v3=0
      fi
      if (( relmaj > 0 )); then (( m != relmaj )) && continue; else (( m < SET_MAJOR )) && continue; fi
      key=$(( m*10000 + v2*100 + v3 ))
      (( key > bestkey )) && { bestkey=$key; best="$f"; }
    done < <(printf '%s' "$html" | grep -oE "${prefix}-([0-9a-z]{3}|[0-9]{6})\.zip" | sort -u)
    if [[ -n "$best" ]]; then
      if fetch -o "$ORIGIN/$rel/$best.part" "${dir}${best}"; then
        mv -f "$ORIGIN/$rel/$best.part" "$ORIGIN/$rel/$best"
        echo "$(date -Is) FALLBACK $url -> $best" >&2
        printf '%s\n' "$ORIGIN/$rel/$best"; return 0
      fi
      rm -f "$ORIGIN/$rel/$best.part"
    fi
  fi
  echo "$(date -Is) FAILDL $url" >&2
  return 1
}
export -f download_zip

# ON-THE-FLY worker: one spec -> download (if missing) -> unzip + convert (if missing)
process_spec() {
  local rel="$1" url="$2" name="$3" zip tmp inner base target
  if [[ "$DO_DOWNLOAD" -eq 1 ]]; then
    zip="$(download_zip "$rel" "$url" "$name")" || zip=""
  else
    zip="$ORIGIN/$rel/$name"; [[ -s "$zip" ]] || zip=""
  fi
  [[ "$DO_CONVERT" -eq 1 && -n "$zip" && -s "$zip" ]] || return 0
  mkdir -p "$CONVERT/$rel"
  tmp="$(mktemp -d)"
  # The ingest classifier (classifyFile/reFile) keys off the CODED filename
  # (NNNNN[-N]-CODE…). The HTML is named after the inner doc — fine for multi-part
  # zips whose inner docs ship coded names (36521-1-i00_s00-s05), but MANY single-doc
  # zips ship a HUMAN inner name ("TR 21.877 v0.7.0.doc") → the converted HTML would
  # NOT classify and is SILENTLY DROPPED at ingest (the draft/old-TR 0-keys bug).
  # So: prefix the HTML base with the ZIP's coded name unless the inner name already
  # starts with it (reFile's optional _suffix then classifies it correctly).
  local zipbase; zipbase="$(basename "$zip")"; zipbase="${zipbase%.zip}"
  # Canonicalise an underscore code-separator (NNNNN_CODE -> NNNNN-CODE): the
  # ingest classifier wants the dash form, so a stray underscore would otherwise
  # leave the file unclassified and silently dropped.
  local recode='s/^([0-9]{4,5}(-[0-9]+)?)_([0-9a-z]{3}|[0-9]{6})/\1-\3/'
  zipbase="$(printf '%s' "$zipbase" | sed -E "$recode")"
  if unzip -qo "$zip" -d "$tmp" 2>/dev/null; then
    # Candidate spec documents, minus: Word owner-lock stubs (._*, ~$* — e.g. the
    # 28552 sample media), and pure readme / release-note placeholders. Some zips
    # (e.g. 55.226) ship ONLY a readme and no spec doc — converting it would index
    # junk under the spec id, so a doc-less zip is skipped explicitly.
    local -a docs=()
    while IFS= read -r f; do docs+=("$f"); done < <(
      find "$tmp" -type f \( -iname '*.doc' -o -iname '*.docx' \) \
        -not -name '._*' -not -name '~$*' \
        -not -iname 'readme*' -not -iname '*release note*' -not -iname '*release-note*')
    if [[ ${#docs[@]} -eq 0 ]]; then
      echo "$(date -Is) SKIPZIP $zip :: no spec document (placeholder/readme only)" >&2
      rm -rf "$tmp"; return 0
    fi
    for inner in "${docs[@]}"; do
      base="$(basename "$inner")"; base="${base%.*}"
      base="$(printf '%s' "$base" | sed -E "$recode")"   # underscore -> dash code sep
      case "$base" in
        "$zipbase"*) : ;;
        *) base="${zipbase}_$(printf '%s' "$base" | tr -cs 'A-Za-z0-9' '-' | sed 's/^-*//;s/-*$//')" ;;
      esac
      target="$CONVERT/$rel/$base.html"
      [[ -s "$target" ]] && continue
      # convert_doc: clean export, else EMF/WMF-strip retry (tagged degraded).
      if convert_doc "$inner" "$target" "$rel/$base"; then
        [[ "$CONV_STATUS" == degraded ]] && echo "$(date -Is) DEGRADED $zip :: $(basename "$inner")" >&2
      else
        echo "$(date -Is) FAILCV $zip :: $(basename "$inner")" >&2
      fi
    done
  fi
  rm -rf "$tmp"
}
export -f process_spec convert_doc _soffice_html
export ORIGIN CONVERT DO_DOWNLOAD DO_CONVERT CONV_TIMEOUT CONV_KILL DEGRADED_TSV

# ----- disk guard -----
free_gb=$(df -BG --output=avail "$ROOT/data/sources" | tail -1 | tr -dc '0-9')
if (( free_gb < MIN_FREE_GB )); then log "ERROR: only ${free_gb}G free (< ${MIN_FREE_GB}G), abort"; exit 1; fi

# ----- soffice presence (needed only if converting) -----
# Normally provided at build by the libreoffice devcontainer feature; this is a
# self-heal net for containers where that didn't run (stale image, /update reset).
if [[ "$DO_CONVERT" -eq 1 ]] && ! command -v soffice >/dev/null 2>&1; then
  log "soffice not found — attempting one-off install of libreoffice-writer ..."
  if command -v sudo >/dev/null 2>&1; then
    sudo DEBIAN_FRONTEND=noninteractive apt-get update -qq \
      && sudo DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends libreoffice-writer >/dev/null 2>&1 || true
  fi
  command -v soffice >/dev/null 2>&1 \
    || { log "ERROR: soffice still missing — install libreoffice-writer or pass --no-convert"; exit 1; }
  log "soffice now available: $(soffice --version 2>/dev/null | head -1)"
fi

# ----- enumerate (status-report-driven; the FIX for the chronic index gap) -----
# The manifest is the FETCH worklist "<release> <url> <name>". We build it from the
# 3GPP status report — the SAME source cmd/discover diffs the published index
# against — so the builder fetches EXACTLY every (spec,release) the site lists
# (drafts v0/v1/v2 INCLUDED) and the index matches the site by construction. The
# old archive-listing + version-major→release heuristic silently dropped ~72% of
# the perpetual delta (drafts skipped by a version-major floor; v3.x of Rel-4
# mis-filed under Rel-99); driving from the report closes that. Legacy GSM 4-digit
# specs are omitted by the report, so they keep the archive enumeration (opt-in).
MANIFEST="$ORIGIN/.manifest.tsv"
if [[ $QUICK -eq 0 || ! -s "$MANIFEST" ]]; then
  log "building fetch worklist from the 3GPP status report (release floor $SET) ..."
  : > "$MANIFEST"
  wl_args=(--emit-worklist --floor "$SET")
  [[ -n "$SERIES_FILTER" ]] && wl_args+=(--series "$SERIES_FILTER")
  if ( cd "$ROOT" && CGO_ENABLED=0 go run ./cmd/discover "${wl_args[@]}" ) >> "$MANIFEST"; then
    log "worklist: $(wc -l < "$MANIFEST") (spec,release) entries from status report"
  else
    log "WARN: status-report worklist failed — falling back to archive enumeration"
    if [[ -n "$SERIES_FILTER" ]]; then SERIES_LIST="$SERIES_FILTER"
    else SERIES_LIST="$(fetch "$BASE/" | grep -oE '[0-9]{2}_series' | sed 's/_series//' | sort -u | tr '\n' ' ')"; fi
    : > "$WORKDIR/pairs"
    for s in $SERIES_LIST; do
      for spec in $(fetch "$BASE/${s}_series/" 2>/dev/null | grep -oE "${s}\.[0-9]{2,3}" | sort -u); do
        printf '%s %s\n' "$s" "$spec" >> "$WORKDIR/pairs"
      done
    done
    xargs -P "$ENUM_JOBS" -n2 bash -c 'emit_spec "$0" "$1"' < "$WORKDIR/pairs" || true
    cat "$WORKDIR"/*.lines 2>/dev/null >> "$MANIFEST" || true
  fi
  # Legacy GSM Phase-1/2 (opt-in): the status report omits 4-digit specs, so the
  # archive enumeration is the only source for them. Append, never replace.
  if [[ "${LEGACY_GSM:-0}" == "1" ]]; then
    legacy_series="$(printf '%s\n' ${SERIES_FILTER:-01 02 03 04 05 06 07 08 09 10 11 12 13} | tr ' ' '\n' | grep -E '^(0[1-9]|1[0-3])$' || true)"
    : > "$WORKDIR/pairs"
    for s in $legacy_series; do
      for spec in $(fetch "$BASE/${s}_series/" 2>/dev/null | grep -oE "${s}\.[0-9]{2,3}" | sort -u); do
        printf '%s %s\n' "$s" "$spec" >> "$WORKDIR/pairs"
      done
    done
    [[ -s "$WORKDIR/pairs" ]] && { xargs -P "$ENUM_JOBS" -n2 bash -c 'emit_spec "$0" "$1"' < "$WORKDIR/pairs" || true; }
    cat "$WORKDIR"/*.lines 2>/dev/null >> "$MANIFEST" || true
  fi
  sort -u -o "$MANIFEST" "$MANIFEST"
fi
TOTAL=$(wc -l < "$MANIFEST" 2>/dev/null || echo 0); TOTAL="${TOTAL//[[:space:]]/}"
log "manifest: $TOTAL files (releases >= $SET)"

# ----- process on the fly (download + unzip + convert per spec) -----
log "processing on the fly (jobs=$JOBS): download -> unzip -> HTML ..."
# Progress bar: converted/total with a percentage + a 20-cell bar. A frozen count
# across ticks = a stall (visible at a glance). Counts HTML in the convert dir, so a
# warm cache legitimately starts above 0.
( while sleep 120; do
    n=$(find "$CONVERT" -name '*.html' 2>/dev/null | wc -l); n="${n//[[:space:]]/}"
    if [[ "${TOTAL:-0}" -gt 0 ]]; then
      pct=$(( n * 100 / TOTAL )); (( pct > 100 )) && pct=100
      fill=$(( pct / 5 )); bar=$(printf '%*s' "$fill" '' | tr ' ' '#')$(printf '%*s' $(( 20 - fill )) '' | tr ' ' '.')
      log "progress: [$bar] ${pct}%  ($n/$TOTAL html)"
    else
      log "progress: $n html so far"
    fi
  done ) &
HB=$!
xargs -P "$JOBS" -n3 bash -c 'process_spec "$0" "$1" "$2"' < "$MANIFEST" || true
kill "$HB" 2>/dev/null || true

touch "$DONE"
log "done. origin: $(find "$ORIGIN" -name '*.zip' | wc -l) zip ($(du -sh "$ORIGIN" 2>/dev/null | cut -f1)); convert: $(find "$CONVERT" -name '*.html' | wc -l) html ($(du -sh "$CONVERT" 2>/dev/null | cut -f1))"
