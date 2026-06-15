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
_soffice_html() {
  local doc="$1" base outdir prof rc html
  base="$(basename "$doc")"; base="${base%.*}"
  outdir="$(mktemp -d)"; prof="$(mktemp -d)"
  timeout -k "$CONV_KILL" "$CONV_TIMEOUT" soffice --headless --norestore \
    -env:UserInstallation="file://$prof" \
    --convert-to html --outdir "$outdir" "$doc" >/dev/null 2>&1
  rc=$?
  pkill -9 -f "$prof" 2>/dev/null || true
  rm -rf "$prof" 2>/dev/null || true
  html="$outdir/$base.html"
  if [[ $rc -eq 0 && -s "$html" ]]; then printf '%s\n' "$html"; return 0; fi
  rm -rf "$outdir" 2>/dev/null || true   # drop crash leftovers (partial gifs, stale lock)
  return 1
}

# convert_pdf <pdf> <target_html> [label] — ETSI deliverables are PUBLISHED as
# digital (text-layer) PDFs. LibreOffice extracts that text layer to HTML — this is
# TEXT EXTRACTION, not OCR (CLAUDE.md §13's no-OCR lock is preserved): a SCANNED,
# image-only PDF yields ~no text and is FAILED here, never OCR'd. Same CONV_STATUS
# contract as convert_doc (clean|fail). The ETSI provenance header is prepended by the
# caller (scripts/etsi-corpus.sh), which knows the id+version from the work-list.
# Env: ETSI_MIN_TEXT (default 200) — min visible chars for a PDF to count as digital.
: "${ETSI_MIN_TEXT:=200}"
convert_pdf() {
  local pdf="$1" target="$2" label="${3:-$2}" produced visible
  mkdir -p "$(dirname "$target")"
  export CONV_STATUS=fail
  # NB: no `local` on this line, or we'd capture local's rc, not soffice's.
  if ! produced="$(_soffice_html "$pdf")"; then
    return 1
  fi
  # Text-layer guard: strip tags and whitespace, count the remaining visible chars.
  # A digital PDF carries the clause text; a scan carries only <img> → near-zero.
  visible="$(sed -e 's/<[^>]*>//g' "$produced" | tr -d '[:space:]' | wc -c | tr -dc '0-9')"
  if [ "${visible:-0}" -lt "$ETSI_MIN_TEXT" ]; then
    rm -rf "$(dirname "$produced")" 2>/dev/null || true
    echo "convert_pdf: $label has no text layer (${visible:-0} chars) — REFUSING (no OCR)" >&2
    return 1   # scanned/image-only PDF: fail honest, never OCR
  fi
  mv -f "$produced" "$target"; rm -rf "$(dirname "$produced")"
  CONV_STATUS=clean; return 0
}

convert_doc() {
  local inner="$1" target="$2" label="${3:-$2}"
  local base produced work tmp n_emf
  mkdir -p "$(dirname "$target")"
  export CONV_STATUS=fail   # output read by callers (corpus.sh / recover-fails.sh)

  # Attempt 1 — straight conversion (the common, clean path).
  # NB: no `local` on this line, or we'd capture local's rc, not soffice's.
  if produced="$(_soffice_html "$inner")"; then
    mv -f "$produced" "$target"; rm -rf "$(dirname "$produced")"
    CONV_STATUS=clean; return 0
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
    if [[ "$n_emf" -gt 0 ]]; then
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
