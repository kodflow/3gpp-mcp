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

  # Attempt 2 — only OOXML .docx can be de-metafiled; legacy OLE .doc cannot.
  case "$inner" in
    *.docx|*.DOCX) ;;
    *) return 1;;
  esac
  tmp="$(mktemp -d)"; base="$(basename "$inner")"; base="${base%.*}"
  work="$tmp/$base.docx"; cp -f "$inner" "$work"
  n_emf="$(unzip -l "$work" 2>/dev/null | grep -icE '\.(emf|wmf|x-emf)$' || true)"
  if [[ "$n_emf" -eq 0 ]]; then rm -rf "$tmp"; return 1; fi   # crash wasn't a metafile
  zip -dq "$work" 'word/media/*.emf' 'word/media/*.wmf' 'word/media/*.x-emf' 2>/dev/null || true
  if produced="$(_soffice_html "$work")"; then
    { printf '<!-- 3GPP-MCP-DEGRADED: emf-wmf-stripped; soffice HTML export crashed on embedded vector metafiles; %s figure(s) omitted; text+tables intact -->\n' "$n_emf"
      cat "$produced"; } > "$target"
    rm -rf "$(dirname "$produced")" "$tmp"
    mkdir -p "$(dirname "$DEGRADED_TSV")"
    printf '%s\t%s\t%s\temf-wmf-stripped\n' "$(date -Is)" "$label" "$n_emf" >> "$DEGRADED_TSV"
    CONV_STATUS=degraded; return 0
  fi

  rm -rf "$tmp"; return 1
}
