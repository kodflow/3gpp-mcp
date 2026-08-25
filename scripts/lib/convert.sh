#!/usr/bin/env bash
#
# convert.sh — shared DOC/DOCX -> HTML conversion for the 3GPP corpus.
#
# Sourced by scripts/corpus.sh and scripts/recover-fails.sh so the conversion
# policy (timeout, crash-retry, degraded tagging) lives in exactly one place.
#
# convert_doc tries a straight LibreOffice HTML export first. Some 3GPP .docx
# (notably TS 33.501 across every release) make soffice ABORT — `Fatal
# exception: Signal 6` inside drawinglayer::MetafilePrimitive2D — while
# rasterising an embedded EMF/WMF vector figure for the HTML filter. That is a
# fast crash, not a timeout, so a bigger timeout never helps. On that failure we
# strip word/media metafiles from the .docx (a zip) and retry: text + tables
# survive, only the vector figures are lost (figures are out of corpus scope,
# CLAUDE.md §7). A degraded result is tagged in two ways so downstream ingest
# never mistakes a figure-less conversion for a complete one:
#   1. an HTML comment "3GPP-MCP-DEGRADED ..." prepended as the first line;
#   2. a row in data/sources/.degraded.tsv.
#
# Contract:
#   convert_doc <inner_doc> <target_html> [label]
#     -> returns 0 on success, 1 on hard failure
#     -> sets CONV_STATUS to one of: clean | degraded | fail
#
# Env knobs: CONV_TIMEOUT (default 900s), CONV_KILL (default 60s), DEGRADED_TSV.

: "${CONV_TIMEOUT:=900}"
: "${CONV_KILL:=60}"

# REMEMBER WHAT SOFFICE CANNOT CONVERT, OR PAY FOR IT ON EVERY RUN.
#
# A document soffice CRASHES on is cheap: it fails in seconds and falls through.
# A document it HANGS on costs CONV_TIMEOUT twice — the straight attempt and the
# EMF-stripped retry — which is 30 minutes for one file, every single run. TS
# 38.141-1 did exactly that: 900 s, then 900 s again after the metafiles were
# removed, to end at the pandoc fallback it was always going to end at.
#
# So record the timeouts and go straight to pandoc next time. This is a CACHE of
# an observation, not a verdict: it lives under .local/state (operational, never
# committed) and deleting the file simply makes the next run re-measure. A
# LibreOffice upgrade is exactly the reason someone would want that.
_CONV_STATE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)/.local/state"
: "${SOFFICE_TIMEOUT_LIST:=$_CONV_STATE_DIR/soffice-timeouts.txt}"

# _conv_key <target_html> — the stable identity of a document across runs. The
# source path is a throwaway temp dir; the target name is the spec and version.
_conv_key() { local b; b="$(basename "$1")"; printf '%s' "${b%.html}"; }

_soffice_known_hang() {
  [[ -s "$SOFFICE_TIMEOUT_LIST" ]] || return 1
  grep -qxF "$(_conv_key "$1")" "$SOFFICE_TIMEOUT_LIST" 2>/dev/null
}

_soffice_note_hang() {
  mkdir -p "$(dirname "$SOFFICE_TIMEOUT_LIST")" 2>/dev/null || return 0
  local k; k="$(_conv_key "$1")"
  grep -qxF "$k" "$SOFFICE_TIMEOUT_LIST" 2>/dev/null || printf '%s\n' "$k" >> "$SOFFICE_TIMEOUT_LIST"
}

# Anchor the degraded manifest to the repo (…/scripts/lib -> …/data/sources)
_CONV_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
: "${DEGRADED_TSV:=$(cd "$_CONV_LIB_DIR/../.." && pwd)/data/sources/.degraded.tsv}"

# _soffice_html <doc> — convert one document to HTML. On success prints the path
# to the produced .html (rc 0); on failure returns 1. CRITICAL: each call uses
# its OWN throwaway output dir AND profile. A crashing soffice leaves a
# `.~lock.<out>.html#` stale lock behind; if a retry reused the same outdir the
# lock would abort its write (Io/Abort Code:27) while soffice still exits 0 —
# i.e. a silent empty conversion. A pristine dir per call removes that trap and
# also isolates parallel workers.
# _conv_native / _conv_url — hand soffice paths its own platform understands.
#
# soffice is a NATIVE Windows binary. Given the POSIX paths this shell deals in
# it exits 0 and writes nothing: no error, no output, and `--convert-to` reports
# success. The caller then finds no HTML, and the whole document falls through
# four increasingly desperate fallbacks — 900 s of timeout each — for a file that
# converts in seconds once the path is right. Measured: 1 000 specs, four
# workers, ten minutes, zero conversions.
#
# The same trap is already documented for CUDA in docs/local-pipeline.md: a
# POSIX-style path is invisible to the Windows loader. It applies to every native
# binary this pipeline drives, not just the one where it was first noticed.
#
# cygpath -m produces `C:/dir/file` (forward slashes), which soffice accepts both
# as an argument and inside a file:// URL. Absent cygpath (Linux, WSL) the paths
# pass through untouched.
_conv_native() {
  if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"; else printf '%s' "$1"; fi
}
_conv_url() {
  if command -v cygpath >/dev/null 2>&1; then
    printf 'file:///%s' "$(cygpath -m "$1")"   # file:///C:/...
  else
    printf 'file://%s' "$1"                     # file:///tmp/... (path is absolute)
  fi
}

# _soffice_profile returns this worker's REUSED LibreOffice profile, creating it
# on first use.
#
# The profile used to be a fresh mktemp per document, and that was 80% of the cost
# of a conversion. Measured on a 6-byte text file — no layout work at all:
#
#     fresh profile ........ 24.0 s
#     reused, 2nd call ...... 5.9 s
#
# Across 1 218 documents on four workers that is roughly 8 worker-hours, ~39% of
# the whole fetch step, spent building the same user profile over and over.
#
# The fresh-per-call rule existed for a real reason, but it protected the wrong
# directory: a crashing soffice leaves a `.~lock.<out>.html#` behind in the OUTPUT
# directory, and reusing THAT makes the next write abort while soffice still exits
# 0 — a silent empty conversion. The outdir therefore stays fresh per call. The
# profile is per WORKER: parallel workers still never share one (BASHPID differs
# in every xargs subshell), and a crash deletes it so the next call rebuilds a
# clean one rather than inheriting a wedged state.
# Keyed on $$ (the WORKER shell), never $BASHPID.
#
# `_soffice_html` is called from a command substitution, so it runs in a subshell
# and $BASHPID is different on every single call — keying on it silently recreated
# the profile each time and the optimisation did nothing. $$ stays the worker's
# pid across its subshells, which is exactly the granularity wanted: one profile
# per parallel worker, shared by all the documents that worker converts.
_soffice_profile() {
  local d="${TMPDIR:-/tmp}/3gpp-soffice-prof-$$"
  [ -d "$d" ] || mkdir -p "$d"
  printf '%s' "$d"
}

_soffice_html() {
  local doc="$1" base outdir prof rc html
  base="$(basename "$doc")"; base="${base%.*}"
  outdir="$(mktemp -d)"; prof="$(_soffice_profile)"
  # An EMPTY profile path is not a degraded conversion, it is a broken worker:
  # -env:UserInstallation=file:// makes soffice refuse with "the user installation
  # could not be completed", and since its output goes to /dev/null the step then
  # reports successful downloads and produces no HTML at all. Fail loudly here
  # rather than let 251 specs pass through as silent no-ops.
  if [[ -z "$prof" ]]; then
    echo "$(date -Is) FATAL soffice profile path is empty — the worker is missing convert.sh helpers; the caller must use convert_export_fns" >&2
    rm -rf "$outdir" 2>/dev/null || true
    return 1
  fi
  timeout -k "$CONV_KILL" "$CONV_TIMEOUT" soffice --headless --norestore \
    -env:UserInstallation="$(_conv_url "$prof")" \
    --convert-to html --outdir "$(_conv_native "$outdir")" "$(_conv_native "$doc")" >/dev/null 2>&1
  rc=$?
  # Published for the caller: 124 is `timeout` saying it killed soffice, which is
  # the expensive failure worth remembering. Any other non-zero is a fast crash.
  CONV_SOFFICE_RC=$rc
  html="$outdir/$base.html"
  if [[ $rc -eq 0 && -s "$html" ]]; then printf '%s\n' "$html"; return 0; fi
  # FAILURE PATH ONLY. A crashed soffice can leave its profile wedged, so drop it
  # and let the next call rebuild a clean one. On SUCCESS the profile is kept —
  # that reuse is the whole optimisation (24.0 s -> 5.9 s per document).
  pkill -9 -f "$prof" 2>/dev/null || true
  rm -rf "$prof" 2>/dev/null || true
  rm -rf "$outdir" 2>/dev/null || true   # drop crash leftovers (partial gifs, stale lock)
  return 1
}

# convert_export_fns exports EVERY function a worker subshell needs, as ONE unit.
#
# xargs/parallel workers are fresh bash processes: they see only what `export -f`
# put in their environment. A caller that exports convert_doc and _soffice_html
# but not their helpers produces "helper: command not found" per document, an
# empty result where a path was expected, and NO HTML — while downloads keep
# reporting success.
#
# This list was maintained by hand at three call sites and drifted twice. The
# second time, 97dfcc9 added _soffice_profile and corpus.sh's list was not
# updated: a 2h20 fetch converted exactly zero documents before anyone noticed,
# because soffice's own complaint is sent to /dev/null.
#
# So the list lives HERE, beside the functions it names, and callers ask for it
# by name. Adding a helper to this file is now the only place it must be
# remembered.
convert_export_fns() {
  export -f convert_doc _soffice_html _soffice_profile _conv_native _conv_url
}

# convert_pdf <pdf> <target_html> [label] — ETSI deliverables are PUBLISHED as
# digital (text-layer) PDFs. We extract the TEXT LAYER with poppler's pdftotext —
# NOT OCR (CLAUDE.md §13's no-OCR lock is preserved): a SCANNED, image-only PDF yields
# ~no text and is FAILED here, never OCR'd. (LibreOffice/Draw is the WRONG tool for
# PDF — it rasterises pages; poppler reads the embedded text.) The flat text is then
# wrapped into the minimal HTML htmlparse expects: lines that begin with a clause
# number ("6.2.3 Title" / "Annex A …") become <h1>"<num>\t<title>"</h1> so the SAME
# clause-aware chunker that handles 3GPP HTML also chunks ETSI; other lines become <p>.
# Same CONV_STATUS contract as convert_doc (clean|fail). The ETSI provenance header is
# prepended by the caller (scripts/etsi-corpus.sh).
# Env: ETSI_MIN_TEXT (default 200) — min visible chars for a PDF to count as digital.
: "${ETSI_MIN_TEXT:=200}"
convert_pdf() {
  local pdf="$1" target="$2" label="${3:-$2}" txt visible
  mkdir -p "$(dirname "$target")"
  export CONV_STATUS=fail
  command -v pdftotext >/dev/null 2>&1 || { echo "convert_pdf: pdftotext (poppler-utils) missing" >&2; return 1; }
  txt="$(mktemp)"
  # -layout keeps the visual column order; -enc UTF-8 for clause text. No OCR option
  # is ever passed — pdftotext only reads an existing text layer.
  if ! pdftotext -layout -enc UTF-8 "$pdf" "$txt" 2>/dev/null; then
    rm -f "$txt"; return 1
  fi
  visible="$(tr -d '[:space:]' <"$txt" | wc -c | tr -dc '0-9')"
  if [ "${visible:-0}" -lt "$ETSI_MIN_TEXT" ]; then
    rm -f "$txt"
    echo "convert_pdf: $label has no text layer (${visible:-0} chars) — REFUSING (no OCR)" >&2
    return 1 # scanned/image-only PDF: fail honest, never OCR
  fi
  # Flat text -> minimal HTML. awk wraps clause-number / Annex lines as <h1> with a TAB
  # between number and title (the "<num>\t<title>" shape htmlparse's heading regex
  # expects); everything else is a <p>. HTML-escape &,<,> first.
  {
    printf '<html><body>\n'
    sed -e 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g' "$txt" | awk '
      # Table-of-contents lines ("3.1   Definitions ......... 9") and page running-
      # headers are NAVIGATION, not clauses: a dot-leader run (….N) duplicates the
      # real heading deeper in the document and would create a bogus, body-less clause
      # that pollutes retrieval. Drop any line carrying a 3+ dot leader entirely.
      /\.\.\.+/ { next }
      # RUNNING HEADERS AND FOOTERS ARE NOT CLAUSES.
      #
      # pdftotext -layout keeps the page furniture, and ETSI stamps every single page
      # with "<page-number>    ETSI TS 103 280 V2.19.1 (2026-08)". The clause-number
      # heuristic below sees a leading integer and promotes each one to a heading:
      # measured on TS 103 280, that produced 151 headings of which only ~11 were real
      # clauses. Retrieval would then cite "clause 47" for what is page 47 — the exact
      # opposite of the cite-exactly rule. Drop the furniture before the heuristic runs.
      /^[[:space:]]*[0-9]*[[:space:]]*ETSI[[:space:]]+T[SR][[:space:]]+[0-9]/ { next }
      /^[[:space:]]*ETSI[[:space:]]*$/                                       { next }
      /^[[:space:]]*[0-9]+[[:space:]]+ETSI[[:space:]]*$/                     { next }
      /^[[:space:]]*[0-9]*[[:space:]]*(Final[[:space:]]+draft[[:space:]]+)?ETSI[[:space:]]+E[SN][[:space:]]+[0-9]/ { next }
      /^[[:space:]]*[0-9]+(\.[0-9]+)*[[:space:]]+[^[:space:]].*/ {
        line=$0; sub(/^[[:space:]]+/, "", line)
        n=line; sub(/[[:space:]].*$/, "", n)           # the clause number
        t=line; sub(/^[^[:space:]]+[[:space:]]+/, "", t) # the title
        # A TOP-LEVEL clause number is small. ETSI and 3GPP never reach 100 at the
        # first level, so a bare 3-digit integer is page furniture or an address —
        # "650  Route des Lucioles" is ETSI’s own postal address in the boilerplate,
        # and it was being indexed as clause 650.
        if (n !~ /\./ && n+0 >= 100) { printf "<p>%s</p>\n", line; next }
        printf "<h1>%s\t%s</h1>\n", n, t; next
      }
      # An annex HEADING is "Annex A", "Annex A:" or "Annex A (normative):" — the
      # letter is followed by punctuation or nothing. A SENTENCE that opens with the
      # same words ("Annex D gives a translation for…") is prose, and indexing it as a
      # clause heading attaches the whole following passage to a clause that does not
      # exist. Require the punctuation.
      /^[[:space:]]*Annex[[:space:]]+[A-Z][0-9]*[[:space:]]*([(:]|$)/ {
        line=$0; sub(/^[[:space:]]+/, "", line); printf "<h1>%s</h1>\n", line; next
      }
      /[^[:space:]]/ { line=$0; sub(/^[[:space:]]+/, "", line); printf "<p>%s</p>\n", line }
    '
    printf '</body></html>\n'
  } >"$target"
  rm -f "$txt"
  CONV_STATUS=clean
  return 0
}

convert_doc() {
  local inner="$1" target="$2" label="${3:-$2}"
  local base produced work tmp n_emf
  mkdir -p "$(dirname "$target")"
  export CONV_STATUS=fail   # output read by callers (corpus.sh / recover-fails.sh)

  # A document soffice is KNOWN to hang on skips straight to pandoc: the two
  # soffice attempts below would cost CONV_TIMEOUT each to reach the same place.
  local skip_soffice=0
  if _soffice_known_hang "$target"; then
    echo "$(date -Is) SOFFICE-SKIP $(_conv_key "$target") :: known to hang; straight to pandoc" >&2
    skip_soffice=1
  fi

  # Attempt 1 — straight conversion (the common, clean path).
  # NB: no `local` on this line, or we'd capture local's rc, not soffice's.
  if [[ $skip_soffice -eq 0 ]]; then
    if produced="$(_soffice_html "$inner")"; then
      mv -f "$produced" "$target"; rm -rf "$(dirname "$produced")"
      CONV_STATUS=clean; return 0
    fi
    # A timeout is the expensive failure: remember it so the next run does not
    # re-buy the same 900 seconds.
    if [[ "${CONV_SOFFICE_RC:-0}" -eq 124 ]]; then
      _soffice_note_hang "$target"
      skip_soffice=1
    fi
  fi

  # A degraded conversion is tagged in two ways (HTML comment + .degraded.tsv).
  _degraded() { # <reason> <count>
    mkdir -p "$(dirname "$DEGRADED_TSV")"
    printf '%s\t%s\t%s\t%s\n' "$(date -Is)" "$label" "${2:-0}" "$1" >> "$DEGRADED_TSV"
    CONV_STATUS=degraded
  }

  case "$inner" in
  *.docx|*.DOCX)
    # Attempt 2 — strip the crash-inducing EMF/WMF metafiles from the .docx (a
    # zip) and retry soffice: text + tables survive, only vector figures are lost.
    tmp="$(mktemp -d)"; base="$(basename "$inner")"; base="${base%.*}"
    work="$tmp/$base.docx"; cp -f "$inner" "$work"
    n_emf="$(unzip -l "$work" 2>/dev/null | grep -icE '\.(emf|wmf|x-emf)$' || true)"
    # Stripping the metafiles helps a soffice that CRASHES on them. It does not
    # help a soffice that hangs: the second attempt would simply spend another
    # CONV_TIMEOUT to arrive at pandoc anyway.
    if [[ "$n_emf" -gt 0 && $skip_soffice -eq 0 ]]; then
      zip -dq "$work" 'word/media/*.emf' 'word/media/*.wmf' 'word/media/*.x-emf' 2>/dev/null || true
      if produced="$(_soffice_html "$work")"; then
        { printf '<!-- 3GPP-MCP-DEGRADED: emf-wmf-stripped; soffice HTML export crashed on embedded vector metafiles; %s figure(s) omitted; text+tables intact -->\n' "$n_emf"
          cat "$produced"; } > "$target"
        rm -rf "$(dirname "$produced")" "$tmp"; _degraded emf-wmf-stripped "$n_emf"; return 0
      fi
    fi
    rm -rf "$tmp"
    # Attempt 3 — pandoc on the original .docx (a different OOXML reader; salvages
    # the docs soffice can't render at all). Tables + headings survive.
    if command -v pandoc >/dev/null 2>&1; then
      if pandoc -f docx -t html --quiet -o "$target.pd" "$inner" 2>/dev/null && [[ -s "$target.pd" ]]; then
        { printf '<!-- 3GPP-MCP-DEGRADED: pandoc-fallback; soffice HTML export failed; text+tables via pandoc -->\n'
          cat "$target.pd"; } > "$target"
        rm -f "$target.pd"; _degraded pandoc-fallback 0; return 0
      fi
      rm -f "$target.pd"
    fi
    return 1
    ;;
  *.doc|*.DOC)
    # Attempt 4 — legacy OLE .doc that soffice crashed on: salvage the NORMATIVE
    # TEXT with antiword/catdoc (no figures, flattened tables, but the clause text
    # is indexed instead of the whole spec being lost). Last resort, tagged.
    local txt=""
    if command -v antiword >/dev/null 2>&1; then txt="$(antiword -w 0 "$inner" 2>/dev/null || true)"; fi
    if [[ -z "$txt" ]] && command -v catdoc >/dev/null 2>&1; then txt="$(catdoc -w "$inner" 2>/dev/null || true)"; fi
    if [[ -n "$txt" ]]; then
      { printf '<!-- 3GPP-MCP-DEGRADED: doc-text-salvage; soffice crashed on legacy .doc; text-only (no tables/figures) -->\n<html><body><pre>\n'
        printf '%s' "$txt" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g'
        printf '\n</pre></body></html>\n'; } > "$target"
      _degraded doc-text-salvage 0; return 0
    fi
    return 1
    ;;
  esac
  return 1
}
