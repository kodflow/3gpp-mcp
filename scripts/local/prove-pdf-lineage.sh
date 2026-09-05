#!/usr/bin/env bash
# THE CITATION ROUND-TRIP: a published PDF in, a dated lineage out.
#
# WHY THIS EXISTS BESIDE `prove-serving.sh`. That proof asks the server what it
# CAN do — it counts the live retrieval arms and asserts each one answers. It
# never leaves the machine, so it cannot tell you whether what the corpus SAYS
# matches what the standards body actually published. This one closes that loop
# from the outside:
#
#   1. ask the MCP where a spec's newest text lives   -> it hands back a URL
#   2. download THAT EXACT PDF from the standards body -> the real artefact
#   3. lift sentences out of the PDF and ask the MCP to place them
#   4. ask WHEN each statement entered the spec        -> trace_clause
#   5. ask WHAT CHANGED between two references        -> trace_clause +/-
#
# Step 2 is the point. The citation is not a decoration: a reader must be able
# to open it and find the text there. If the corpus drifted from the published
# document, or the URL builder is wrong, this fails where every local gate stays
# green — which is the failure mode this project keeps paying for.
#
# BOTH AXES ARE EXERCISED, because they are not the same code path. A 3GPP spec
# evolves along RELEASE; an ETSI deliverable has no releases and evolves along
# VERSION (TS 102 221 has 126 of them). trace_clause picks the axis from the
# spec and reports which one it used; a regression that collapsed the two would
# still answer, just wrongly, so the assertions check the axis BY NAME.
#
# The assertions live in assert-pdf-lineage.sh so they can be re-run against a
# saved transcript with no server and no network — the only way to know an
# assertion can fail is to run it on a transcript where it must.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
. scripts/local/toolchain-env.sh >/dev/null 2>&1

# The PDF half: an ETSI deliverable, published as a PDF, with a long version
# history. The release half: a 3GPP spec whose clause spans several releases.
SPEC_PDF="${SPEC_PDF:-ETSI TS 102 221}"
SPEC_REL="${SPEC_REL:-23.501}"
CLAUSE_REL="${CLAUSE_REL:-5.4.4a}"
FROM_REL="${FROM_REL:-Rel-17}"
TO_REL="${TO_REL:-Rel-18}"
N_CAND="${N_CAND:-8}"

WORK="$ROOT/.local/pdf-lineage"
OUT="$WORK/transcript.jsonl"
FACTS="$WORK/facts.env"
mkdir -p "$WORK"
: >"$OUT"
: >"$WORK/server.err"

say() { printf '\n\033[1m%s\033[0m\n' "$*"; }
die() { echo "PROVE-PDF FAILED: $*" >&2; exit 1; }

# Values arrive as JSON nested inside a JSON string, so every quote is escaped.
# The `.?` absorbs the backslash when it is there and matches when it is not —
# the same shape assert-serving.sh uses, for the same reason.
field() { grep -oE "\"$1.?\": .?\"[^\\\\]*" | head -1 | sed 's/.*\\"//; s/.*": "//'; }

# ------------------------------------------------------------------ phase 0
# WHAT IS UNDER TEST. Prefer the published image when a container runtime is
# present: that is the artefact users actually get, corpus included. This
# machine has no runtime (no Docker, no Podman, no WSL distro), so it falls back
# to the local binary over the local corpus — the same code and the same data,
# but NOT a proof that the container starts. The transcript records which one
# ran so a reader can never mistake one for the other.
say "[0] resolving the MCP under test"
RUNTIME=""
for rt in docker podman; do
  command -v "$rt" >/dev/null 2>&1 && { RUNTIME="$rt"; break; }
done
IMAGE="${MCP_IMAGE:-ghcr.io/kodflow/3gpp-mcp:latest}"

if [ -n "$RUNTIME" ] && [ "${MCP_LOCAL:-0}" != "1" ]; then
  echo "  runtime  $RUNTIME"
  echo "  pulling  $IMAGE"
  "$RUNTIME" pull "$IMAGE" >/dev/null || die "could not pull $IMAGE"
  UNDER_TEST="image $IMAGE via $RUNTIME"
  rpc() { "$RUNTIME" run -i --rm "$IMAGE" serve 2>>"$WORK/server.err"; }
else
  BIN="$ROOT/.local/bin/server-full.exe"
  [ -x "$BIN" ] || BIN="$ROOT/.local/bin/server.exe"
  [ -x "$BIN" ] || die "no server binary; run: make build/build-serve"
  echo "  no container runtime here — driving the LOCAL binary over the LOCAL corpus"
  echo "  binary   $BIN"
  # Best effort: name the image this corpus was published as, so the transcript
  # still records which artefact the data corresponds to. Not a container start.
  CRANE="$ROOT/.local/bin/crane.exe"
  if [ -x "$CRANE" ]; then
    DIG=$(MSYS2_ARG_CONV_EXCL='*' "$CRANE" digest "$IMAGE" 2>/dev/null)
    [ -n "$DIG" ] && echo "  published as (NOT started here): $IMAGE $DIG"
  fi
  UNDER_TEST="local $(basename "$BIN") over data/3gpp.duckdb + data/etsi.duckdb"
  rpc() {
    "$BIN" serve -no-update --db data/3gpp.duckdb --etsi-db data/etsi.duckdb \
      2>>"$WORK/server.err"
  }
fi
echo "  under test: $UNDER_TEST"

hdr() {
  echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"prove-pdf","version":"1"}}}'
  echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
}

# ------------------------------------------------------------------ phase 1
say "[1] asking the MCP where $SPEC_PDF is published"
{
  hdr
  printf '{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"list_releases","arguments":{"spec_id":"%s"}}}\n' "$SPEC_PDF"
} | rpc >>"$OUT"

L10=$(grep '"id":10' "$OUT")
[ -n "$L10" ] || die "list_releases returned nothing for $SPEC_PDF"
VER_NEW=$(printf '%s' "$L10" | field version)
PDF_URL=$(printf '%s' "$L10" | field docx_url)
# Versions come back newest-first, so the last one listed is the oldest held.
VER_OLD=$(printf '%s' "$L10" | grep -oE '"version.?": .?"[^\\]*' | tail -1 | sed 's/.*\\"//')
N_VERS=$(printf '%s' "$L10" | grep -oE '"version.?": .?"[^\\]*' | wc -l | tr -d ' ')
echo "  newest   $VER_NEW   ($N_VERS versions held; oldest $VER_OLD)"
echo "  cites    $PDF_URL"
case "$PDF_URL" in
  http*.pdf) ;;
  *) die "the citation is not a PDF URL: '$PDF_URL'" ;;
esac

# ------------------------------------------------------------------ phase 2
say "[2] downloading that exact PDF and reading its text layer"
PDF="$WORK/spec.pdf"
TXT="$WORK/spec.txt"
# ETSI's CDN refuses a default curl UA; mirror what scripts/etsi-corpus.sh sends.
curl -sSL --max-time 300 -A "Mozilla/5.0 (Windows NT 10.0; Win64; x64)" \
  -H "Accept: application/pdf,*/*" -o "$PDF" "$PDF_URL" \
  || die "could not download $PDF_URL"
[ -s "$PDF" ] || die "downloaded an empty file from $PDF_URL"
head -c 5 "$PDF" | grep -q '%PDF' || die "what the citation points at is not a PDF"
command -v pdftotext >/dev/null 2>&1 \
  || die "pdftotext (poppler) is required to read the PDF and is not on PATH"
pdftotext -layout "$PDF" "$TXT" || die "pdftotext failed on $PDF"
PDF_BYTES=$(wc -c <"$PDF" | tr -d ' ')
TXT_LINES=$(wc -l <"$TXT" | tr -d ' ')
echo "  $PDF_BYTES bytes of PDF -> $TXT_LINES lines of text"

# ------------------------------------------------------------------ phase 3
say "[3] lifting sentences out of the PDF and asking the MCP to place them"
# Deterministic selection, sanitised to a character class that needs no JSON
# escaping — the query is a bag of words to BM25, so dropping punctuation costs
# nothing and removes a whole class of quoting bugs from this harness.
# NR>2000 skips the table of contents; the dot-leader test drops any TOC line
# that offset missed; NR%37 spreads the picks across the whole document instead
# of taking eight consecutive lines out of one clause.
awk -v n="$N_CAND" '
  NR>2000 && length($0)>95 && length($0)<200 && /shall/ && $0 !~ /\.\.\./ && $0 !~ /\|/ {
    gsub(/[^A-Za-z0-9 ,.;:()\/%-]/, " "); gsub(/  +/, " "); sub(/^ +/, ""); print
  }' "$TXT" | awk 'NR%37==1' | head -"$N_CAND" >"$WORK/candidates.txt"
NC=$(wc -l <"$WORK/candidates.txt" | tr -d ' ')
[ "$NC" -ge 4 ] || die "only $NC usable sentences found in the PDF text layer"

{
  hdr
  i=100
  while IFS= read -r s; do
    i=$((i + 1))
    printf '{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"search_spec","arguments":{"query":"%s","spec_type":"any","top_k":3}}}\n' "$i" "$s"
  done <"$WORK/candidates.txt"
} | rpc >>"$OUT"

HITS=0
CLAUSE=""
i=100
while IFS= read -r s; do
  i=$((i + 1))
  line=$(grep "\"id\":$i" "$OUT")
  # The resource URI carries spec, release, clause and version in ONE token:
  #   3gpp://<spec>/<release>/<clause>@<version>
  # Reading the bare "clause" field instead picks whichever citation happens to
  # be first, which is regularly a DIFFERENT spec — 3GPP conformance specs
  # (51.010-1) quote UICC requirements verbatim and legitimately outrank the
  # source. Matching the URI by spec is the only form that answers "where did
  # THIS spec put it", and getting that wrong makes the next phase trace a
  # clause path that belongs to another document.
  res=$(printf '%s' "$line" | grep -oE "3gpp://$SPEC_PDF/[^\\\\\"]*" | head -1)
  if [ -n "$res" ]; then
    HITS=$((HITS + 1))
    c=${res##*/}
    c=${c%%@*}
    [ -n "$CLAUSE" ] || CLAUSE="$c"
    printf '  placed    %-18s %s\n' "$c" "$(printf '%s' "$s" | cut -c1-56)"
  else
    printf '  elsewhere %-18s %s\n' "$(printf '%s' "$line" | field spec_id)" \
      "$(printf '%s' "$s" | cut -c1-56)"
  fi
done <"$WORK/candidates.txt"
echo "  $HITS of $NC sentences placed in $SPEC_PDF"
[ -n "$CLAUSE" ] || die "not one sentence from the PDF was placed in $SPEC_PDF"

# ------------------------------------------------------------------ phase 4
say "[4] dating clause $CLAUSE, and diffing two references on both axes"
echo "  version axis  $SPEC_PDF $CLAUSE   $VER_OLD -> $VER_NEW"
echo "  release axis  $SPEC_REL $CLAUSE_REL   $FROM_REL -> $TO_REL"
{
  hdr
  printf '{"jsonrpc":"2.0","id":200,"method":"tools/call","params":{"name":"trace_clause","arguments":{"spec_id":"%s","clause":"%s"}}}\n' "$SPEC_PDF" "$CLAUSE"
  printf '{"jsonrpc":"2.0","id":201,"method":"tools/call","params":{"name":"trace_clause","arguments":{"spec_id":"%s","clause":"%s","from_release":"%s","to_release":"%s"}}}\n' "$SPEC_PDF" "$CLAUSE" "$VER_OLD" "$VER_NEW"
  printf '{"jsonrpc":"2.0","id":300,"method":"tools/call","params":{"name":"trace_clause","arguments":{"spec_id":"%s","clause":"%s"}}}\n' "$SPEC_REL" "$CLAUSE_REL"
  printf '{"jsonrpc":"2.0","id":301,"method":"tools/call","params":{"name":"trace_clause","arguments":{"spec_id":"%s","clause":"%s","from_release":"%s","to_release":"%s"}}}\n' "$SPEC_REL" "$CLAUSE_REL" "$FROM_REL" "$TO_REL"
} | rpc >>"$OUT"

# The FIRST introduction point in the payload, which belongs to the first
# paragraph of the clause — not necessarily to the sentence that was lifted from
# the PDF. A clause carries several paragraphs, each with its own lineage, and
# which one comes back first is not fixed. Say what this number is; the full
# per-paragraph lineage is in the transcript.
INTRO=$(grep '"id":200' "$OUT" | field introduced)
echo "  first paragraph of $CLAUSE entered at: ${INTRO:-<none reported>}"

# Every value is quoted. UNDER_TEST and SPEC_PDF both contain spaces, and this
# file is SOURCED by the assert script: unquoted, "local server-full.exe over …"
# is a command, and the shell duly tried to run server-full.exe.
cat >"$FACTS" <<EOF
UNDER_TEST='$UNDER_TEST'
SPEC_PDF='$SPEC_PDF'
SPEC_REL='$SPEC_REL'
CLAUSE='$CLAUSE'
CLAUSE_REL='$CLAUSE_REL'
VER_NEW='$VER_NEW'
VER_OLD='$VER_OLD'
N_VERS='$N_VERS'
PDF_URL='$PDF_URL'
PDF_BYTES='$PDF_BYTES'
CANDIDATES='$NC'
HITS='$HITS'
INTRODUCED='$INTRO'
FROM_REL='$FROM_REL'
TO_REL='$TO_REL'
EOF

# ------------------------------------------------------------------ phase 5
say "[5] assertions"
bash "$ROOT/scripts/local/assert-pdf-lineage.sh" "$OUT" "$FACTS"
rc=$?
[ "$rc" -eq 0 ] || die "assertions failed — transcript in $OUT"

echo
echo "PROVE-PDF OK — a published PDF was placed, dated and diffed by the MCP"
echo "  under test $UNDER_TEST"
echo "  transcript $OUT"
echo "  facts      $FACTS"
