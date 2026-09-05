#!/usr/bin/env bash
# Assertions over a captured prove-pdf-lineage transcript + its facts file.
#
# Split out of the driver for the same reason assert-serving.sh is: an assertion
# you have never seen FAIL is not an assertion, it is a comment. Both inputs are
# saved artefacts, so this runs with no server, no corpus and no network — which
# is how each check below was falsified before being kept.
#
# HOW THESE WERE FALSIFIED (2026-09-05, against doctored copies of a real run).
# Each doctored input failed EXACTLY ONE check, and the unmodified pair still
# reported ASSERT OK — a doctoring that trips three checks proves nothing about
# which one was watching.
#
#   axis collapsed     sed 's/"axis": "version"/"axis": "release"/' on the
#                      transcript -> only "ETSI traced on the VERSION axis"
#                      fails. This is the check that catches a regression
#                      folding the two axes into one, which would otherwise
#                      still answer — just wrongly.
#   placement collapsed HITS='2' in the facts file -> only the majority check
#                      fails. (The rate is carried in the facts, not the
#                      transcript, so this is where a corpus that stopped
#                      matching the published document shows up.)
#   diff always empty  rewriting both diffs' "added"/"removed" arrays to null
#                      -> only "at least one of the two diffs reports real
#                      added/removed" fails. A build where trace_clause always
#                      answered "nothing changed" cannot pass this.
#
# The escaped-quote shape `"key.?": .?"` is deliberate: the payload is JSON
# nested inside a JSON string, so quotes arrive as \" — `.?` absorbs the
# backslash when it is there and matches when it is not.
set -uo pipefail
OUT="${1:?usage: assert-pdf-lineage.sh <transcript.jsonl> <facts.env>}"
FACTS="${2:?usage: assert-pdf-lineage.sh <transcript.jsonl> <facts.env>}"
[ -r "$OUT" ] || { echo "no transcript at $OUT" >&2; exit 2; }
[ -r "$FACTS" ] || { echo "no facts at $FACTS" >&2; exit 2; }
# shellcheck disable=SC1090
. "$FACTS"
# A truncated facts file is itself a finding, so default every field rather than
# dying on `set -u` halfway through the report: the checks below then FAIL and
# say which field was missing, instead of the script exiting with no verdict.
for v in UNDER_TEST SPEC_PDF SPEC_REL CLAUSE CLAUSE_REL VER_NEW VER_OLD \
  N_VERS PDF_URL PDF_BYTES CANDIDATES HITS INTRODUCED FROM_REL TO_REL; do
  eval ": \${$v:=}"
done

fail=0
ok()   { printf '  OK      %s\n' "$*"; }
bad()  { printf '  FAILED  %s\n' "$*"; fail=$((fail + 1)); }
idline() { grep "\"id\":$1" "$OUT" | head -1; }

check() { # check <label> <condition-result>
  if [ "$2" -eq 0 ]; then ok "$1"; else bad "$1"; fi
}

echo "===== what was under test ====="
if [ -n "${UNDER_TEST:-}" ]; then ok "recorded: $UNDER_TEST"
else bad "the transcript does not record what was driven"; fi

echo "===== the citation resolves to a real published document ====="
case "${PDF_URL:-}" in
  http*.pdf) ok "the MCP cited a PDF URL: $PDF_URL" ;;
  *) bad "the MCP did not cite a PDF URL (got '${PDF_URL:-}')" ;;
esac
# A stub, an error page or a redirect-to-HTML is measured in kilobytes; a real
# ETSI deliverable is measured in megabytes. 100 kB is far below any real spec
# and far above anything the CDN returns when it is refusing.
if [ "${PDF_BYTES:-0}" -ge 100000 ]; then
  ok "downloaded $PDF_BYTES bytes of PDF from that citation"
else
  bad "the citation returned only ${PDF_BYTES:-0} bytes — not a published spec"
fi

echo "===== the PDF's own text is findable in the corpus ====="
# A strict majority, not all of them. Measured 7 of 8 on TS 102 221 v18.4.0. The
# one that misses is not a defect: 3GPP conformance specs (51.010-1) quote UICC
# requirements verbatim, so they legitimately outrank the source document for a
# bag-of-words query. Demanding 8/8 would make this harness fail on a corpus that
# got MORE complete, which is the wrong direction to be strict in.
if [ "${CANDIDATES:-0}" -gt 0 ] && [ $(( ${HITS:-0} * 2 )) -gt "${CANDIDATES:-0}" ]; then
  ok "${HITS}/${CANDIDATES} sentences lifted from the PDF were placed in $SPEC_PDF"
else
  bad "only ${HITS:-0}/${CANDIDATES:-0} sentences were placed in $SPEC_PDF"
fi
[ -n "${CLAUSE:-}" ] && ok "a clause path came back: $CLAUSE" \
  || bad "no clause path came back from the placement"

echo "===== when did it appear (id=200) ====="
L200=$(idline 200)
if [ -n "$L200" ]; then
  ok "trace_clause answered for $SPEC_PDF $CLAUSE"
  # THE AXIS IS THE ASSERTION. An ETSI deliverable has no releases; it evolves
  # along VERSION. A build that traced it on the release axis would still return
  # a well-formed answer, so only the axis name catches it.
  printf '%s' "$L200" | grep -qE '"axis.?": .?"version' \
    && ok "ETSI traced on the VERSION axis" \
    || bad "ETSI was NOT traced on the version axis"
  printf '%s' "$L200" | grep -q 'axis_values' \
    && ok "the axis points are enumerated" \
    || bad "no axis_values in the lineage"
  # This is the first paragraph's entry point, not necessarily the entry point
  # of the sentence lifted from the PDF — a clause holds several paragraphs and
  # each has its own lineage. What is asserted is that the lineage DATES things
  # at all; which paragraph the number belongs to is in the transcript.
  [ -n "${INTRODUCED:-}" ] \
    && ok "the lineage is dated (first paragraph entered at ${INTRODUCED})" \
    || bad "the lineage names no introduction point"
  # At least one paragraph must still be live: the sentences were lifted from
  # the NEWEST published PDF, so if every paragraph of that clause reads as
  # obsolete, the corpus and the document disagree about the present.
  printf '%s' "$L200" | grep -qE '"obsolete.?": false' \
    && ok "at least one paragraph is still present in the newest version" \
    || bad "every paragraph of $CLAUSE reads as obsolete, but the text is in the newest PDF"
else
  bad "trace_clause (id=200) did not answer"
fi

echo "===== what changed between two references ====="
L201=$(idline 201)
L301=$(idline 301)
if [ -n "$L201" ]; then
  ok "version-axis diff answered ($VER_OLD -> $VER_NEW)"
  printf '%s' "$L201" | grep -qE '"unchanged_paragraphs.?":' \
    && ok "it reports how much stood still" \
    || bad "no unchanged_paragraphs in the version-axis diff"
  printf '%s' "$L201" | grep -qE "\"from.?\": .?\"$VER_OLD" \
    && ok "it echoes the requested lower bound" \
    || bad "the version-axis diff does not echo from=$VER_OLD"
else
  bad "version-axis diff (id=201) did not answer"
fi

L300=$(idline 300)
if [ -n "$L300" ]; then
  ok "trace_clause answered for $SPEC_REL $CLAUSE_REL"
  # The mirror of the ETSI check: a 3GPP spec MUST come back on the release axis.
  # Together these two are what prove the axis is chosen per spec rather than
  # hardcoded — either one alone passes on a build that always answers the same.
  printf '%s' "$L300" | grep -qE '"axis.?": .?"release' \
    && ok "3GPP traced on the RELEASE axis" \
    || bad "3GPP was NOT traced on the release axis"
else
  bad "trace_clause (id=300) did not answer"
fi

if [ -n "$L301" ]; then
  ok "release-axis diff answered ($FROM_REL -> $TO_REL)"
  printf '%s' "$L301" | grep -qE "\"to.?\": .?\"$TO_REL" \
    && ok "it echoes the requested upper bound" \
    || bad "the release-axis diff does not echo to=$TO_REL"
else
  bad "release-axis diff (id=301) did not answer"
fi

# An unchanged diff renders "added": null. If BOTH diffs come back null, this
# harness has not actually seen the +/- machinery produce anything, and a build
# that always answered "nothing changed" would pass every check above.
if printf '%s%s' "$L201" "$L301" | grep -qE '"(added|removed).?": .?\['; then
  ok "at least one of the two diffs reports real added/removed paragraphs"
else
  bad "neither diff produced any added or removed paragraph"
fi

echo "===== no errors ====="
if grep -q '"error"' "$OUT"; then
  bad "the transcript carries a JSON-RPC error"
else
  ok "no JSON-RPC error in the transcript"
fi

echo
[ "$fail" -eq 0 ] || { echo "ASSERT FAILED: $fail check(s)" >&2; exit 1; }
echo "ASSERT OK"
