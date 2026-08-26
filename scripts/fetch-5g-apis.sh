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
#   ./scripts/fetch-5g-apis.sh all             # REL-15 .. REL-20 (static list)
#   ./scripts/fetch-5g-apis.sh auto            # EVERY REL-* branch the Forge has
#                                              # RIGHT NOW (REL-15..whatever is live,
#                                              # incl. a freshly-published REL-20/21/…)
#                                              # → a cron build picks up new releases
#                                              #   with ZERO code change.
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

# A working Python 3 is NOT a given here. The Windows toolchain provisions none,
# and `python3` on PATH is the Microsoft Store stub: it prints an advert and
# exits non-zero. Every JSON pipe below then yields an empty string and the fetch
# reports success over nothing — the silent-skip failure this corpus has already
# paid for twice. Resolve one interpreter ONCE, and refuse loudly if there is none.
PY=""
for cand in "${PYTHON:-}" python3 python "$ROOT/.local/toolchain/libreoffice/program/python.exe"; do
  [[ -n "$cand" ]] || continue
  if "$cand" -c 'import json,re,sys' >/dev/null 2>&1; then PY="$cand"; break; fi
done
if [[ -z "$PY" ]]; then
  warn "no working Python 3 (tried \$PYTHON, python3, python, the bundled LibreOffice runtime)"
  warn "set PYTHON=/path/to/python3 and re-run — refusing to fetch into empty JSON"
  exit 1
fi

# A Windows Python ends every print() with CRLF, and $( ) strips the LF but keeps
# the CR. That invisible byte rode into every filename and every SHA: the file
# existence test missed files that were on disk, and the URLs ended in %0D, so
# curl reported http=000 for 478 blobs that answered 200 by hand. Same family as
# the soffice trap — a native tool fed POSIX assumptions succeeds while producing
# something unusable. Every read of the interpreter goes through py().
py() { "$PY" "$@" | tr -d '\r'; }

# One try per file is one try too few over a network this long-running: the fetch
# spans thousands of requests, so a single reset costs a file the manifest then
# records as missing forever. Retry with backoff, and never retry a 403/404: on
# 3GPP hosts 403 IS "not found" (commit 28312b5 measured fetch at 54m35 -> 4m10
# on that lesson).
TMPBODY="$(mktemp 2>/dev/null || echo "${TMPDIR:-/tmp}/5g-apis-$$.body")"
trap 'rm -f "$TMPBODY"' EXIT

# fetch_to URL OUT -> 0 and OUT written non-empty, or 1.
# FETCH_WHY carries the reason of the last failure: a caller that only says
# "download failed" sends the next reader hunting for a bug that may be a quota.
FETCH_WHY=""
fetch_to() {
  local url="$1" out="$2" attempt=1 code
  while :; do
    code="$(curl -sS -A "$UA" --max-time 120 -w '%{http_code}' -o "$out" "$url" 2>/dev/null)"
    if [[ "$code" == "200" && -s "$out" ]]; then FETCH_WHY=""; return 0; fi
    FETCH_WHY="http=${code:-none} after ${attempt} attempt(s)"
    case "$code" in 403|404) rm -f "$out"; return 1 ;; esac
    if (( attempt >= 5 )); then rm -f "$out"; return 1; fi
    # A quota window is measured in minutes, not seconds: back off far enough to
    # outlast one instead of spending five attempts inside the same window.
    sleep $(( attempt * attempt * 5 ))
    (( attempt++ ))
  done
}

# get URL -> body on stdout (same retry policy)
get() { fetch_to "$1" "$TMPBODY" && cat "$TMPBODY"; }

# releases to fetch
releases=("$@")
if [[ ${#releases[@]} -eq 0 ]]; then releases=("REL-18"); fi
if [[ "${releases[0]}" == "all" ]]; then
  releases=(REL-15 REL-16 REL-17 REL-18 REL-19 REL-20)
fi

# list_release_branches -> prints every REL-<n> branch currently on the Forge
# (paged). This is what makes `auto` self-updating: a release published after this
# code was written shows up as a new REL-NN branch and is fetched without any edit.
list_release_branches() {
  local page=1 names
  while :; do
    names=$(get "$API/branches?per_page=100&page=$page" \
      | py -c 'import sys,json,re
try: d=json.loads(sys.stdin.read(), strict=False)  # commit messages carry raw control chars
except Exception: d=[]
[print(b["name"]) for b in d if re.match(r"^REL-[0-9]+$", b.get("name",""))]' 2>/dev/null)
    [[ -z "$names" ]] && break
    printf '%s\n' "$names"
    (( page++ ))
    (( page > 20 )) && break   # safety bound
  done
}

# resolve_sha REF -> prints the head commit SHA of REF, or empty on failure.
resolve_sha() {
  local ref="$1"
  # strict=False for the same reason as the branch listing: a commit message with
  # a raw control character makes a strict json.load throw, and the SHA would be
  # reported as unresolvable for a branch that answered 200.
  get "$API/branches/$ref" \
    | py -c 'import sys,json;print(json.loads(sys.stdin.read(), strict=False)["commit"]["id"])' 2>/dev/null
}

# list_yaml REF -> prints all *.yaml blob names at REF (paged, 100/page).
list_yaml() {
  local ref="$1" page=1 names
  while :; do
    names=$(get "$API/tree?ref=$ref&per_page=100&page=$page" \
      | py -c 'import sys,json
d=json.loads(sys.stdin.read(), strict=False)
[print(x["name"]) for x in d if x["type"]=="blob" and x["name"].endswith(".yaml")]' 2>/dev/null)
    [[ -z "$names" ]] && break
    printf '%s\n' "$names"
    (( page++ ))
    (( page > 30 )) && break   # safety bound (~3000 files)
  done
}

# auto: replace the request with every REL-* branch the Forge currently exposes
# (captures releases BEFORE the hardcoded floor and any NEW one, e.g. REL-20/21).
if [[ "${releases[0]}" == "auto" ]]; then
  mapfile -t releases < <(list_release_branches | sort -u)
  if [[ ${#releases[@]} -eq 0 ]]; then
    warn "auto: no REL-* branches resolved from the Forge — falling back to REL-18"
    releases=(REL-18)
  fi
  log "auto-detected ${#releases[@]} release branch(es): ${releases[*]}"
fi

total_files=0
total_missing=0
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

  # ONE request per release, not one per file: the archive endpoint hands over the
  # whole tree at that SHA in a single call, so a full six-release fetch costs ~40
  # requests instead of ~3000 — minutes instead of an hour, and no chance of the
  # run dying two thirds of the way through a release. Per-file download stays as
  # the fallback for anything the archive does not carry.
  n=0
  got=()                                                  # files actually on disk
  arc="$dest/.archive.zip"
  if [[ ${#files[@]} -gt 0 ]] && fetch_to "$API/archive.zip?sha=$sha" "$arc"; then
    if unzip -qo "$arc" -d "$dest/.unpacked" 2>/dev/null; then
      while IFS= read -r src; do
        [[ -f "$src" ]] || continue
        cp -f "$src" "$dest/$(basename "$src")"
      done < <(find "$dest/.unpacked" -type f -name '*.yaml' 2>/dev/null)
      log "$ref: archive unpacked in one request"
    else
      warn "$ref: the archive does not unzip — falling back to per-file downloads"
    fi
  else
    warn "$ref: no archive at $sha — falling back to per-file downloads"
  fi
  rm -rf "$dest/.unpacked" "$arc"

  for f in "${files[@]}"; do
    out="$dest/$f"
    if [[ -s "$out" ]]; then (( n++ )); got+=("$f"); continue; fi   # archive or a previous run
    if fetch_to "$RAW/$sha/$f" "$out"; then
      (( n++ )); got+=("$f")
    else
      warn "$ref: download failed for $f ($FETCH_WHY)"
      rm -f "$out"
    fi
  done

  # manifest records REALITY: expected (listed) vs downloaded (on disk) vs missing,
  # so a partial loss is auditable instead of hidden behind the expected list.
  DOWNLOADED="$(printf '%s\n' ${got[@]+"${got[@]}"})" \
  "$PY" - "$dest/../manifest.json" "$rel" "$ref" "$sha" "${files[@]}" <<'PY'
import json,os,sys,datetime
out,rel,ref,sha=sys.argv[1:5]; expected=sorted(sys.argv[5:])
got=sorted(x for x in os.environ.get("DOWNLOADED","").splitlines() if x)
missing=sorted(set(expected)-set(got))
json.dump({"release":rel,"ref":ref,"sha":sha,
          "expected":expected,"downloaded":got,"missing":missing,
          "expected_count":len(expected),"downloaded_count":len(got),"missing_count":len(missing),
          "files":got,  # back-compat: ingest reads the files actually present
          "fetched_at":datetime.datetime.now(datetime.timezone.utc).isoformat()},
         open(out,"w"),indent=2)
PY
  miss=$(( ${#files[@]} - n ))
  log "$rel: $n/${#files[@]} YAML files in $dest (missing=$miss)"
  if (( miss > 0 )); then
    # GitHub annotation (visible in the run summary) without blocking the build.
    printf '::warning title=5g-apis incomplete::%s missing %d/%d OpenAPI YAML files\n' \
      "$rel" "$miss" "${#files[@]}" >&2
  fi
  (( total_files += n ))
  (( total_missing += miss ))
done

log "done — $total_files YAML files under $OUT (total missing=$total_missing)"
if (( total_missing > 0 )); then
  printf '::warning title=5g-apis incomplete::%d OpenAPI YAML files missing across releases\n' \
    "$total_missing" >&2
  # Opt-in hard failure for CI that wants OpenAPI completeness to gate the build.
  if [[ "${FETCH_APIS_STRICT:-0}" == "1" ]]; then
    warn "FETCH_APIS_STRICT=1 and $total_missing files missing — failing"
    exit 1
  fi
fi
