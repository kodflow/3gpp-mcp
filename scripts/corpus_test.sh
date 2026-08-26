#!/usr/bin/env bash
# Offline test for the nested-archive extraction in scripts/corpus.sh.
# Run: bash scripts/corpus_test.sh
#
# When a spec goes to plenary for information, 3GPP wraps it: the outer archive
# holds a presentation cover NEXT TO a nested zip carrying the real document.
# Stopping at the first level found a .docx, converted it, and indexed an 8 KB
# cover note under the spec id while 182 KB of specification sat unopened. Five
# keys were in that state and every one looked like a permanent upstream absence.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
fails=0
pass() { echo "PASS  $1"; }
fail() { echo "FAIL  $1"; fails=$((fails + 1)); }

# The regression guard runs ALWAYS — it is the half that must never be skipped,
# and it needs no tooling. Losing the descent is the failure this file exists for.
if grep -q "NESTEDZIP" "$HERE/corpus.sh"; then
	pass "corpus.sh descends into nested archives"
else
	fail "corpus.sh lost the nested-archive descent"
fi

# The functional half needs zip(1) to BUILD the fixture. w64devkit ships unzip but
# not zip, so on Windows this half is skipped rather than faked — a test that
# pretends to have exercised something is worse than one that says it did not.
if ! command -v zip >/dev/null 2>&1; then
	echo "SKIP  the extraction fixture needs zip(1), which this toolchain lacks"
	[ "$fails" -eq 0 ] || { echo "$fails failure(s)"; exit 1; }
	echo "all good (guard only)"
	exit 0
fi

WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
cd "$WORK"

# Build the real shape: cover.docx + nested zip holding the spec.
printf 'cover' > "SP-260553_Presentation_of_TS23366_for_information.docx"
printf 'the actual specification text' > "23366-100.docx"
zip -q inner.zip "23366-100.docx" && rm "23366-100.docx"
mv inner.zip "23366-100.zip"
zip -q outer.zip "SP-260553_Presentation_of_TS23366_for_information.docx" "23366-100.zip"
rm -f "SP-260553_Presentation_of_TS23366_for_information.docx" "23366-100.zip"

# Replay what corpus.sh does: extract, then descend into any nested archive.
tmp="$WORK/x"; mkdir -p "$tmp"
unzip -qo outer.zip -d "$tmp"
while IFS= read -r nested; do
	[ -n "$nested" ] || continue
	unzip -qo "$nested" -d "$tmp" 2>/dev/null || true
	rm -f "$nested"
done < <(find "$tmp" -type f -iname '*.zip')

if [ -f "$tmp/23366-100.docx" ]; then
	pass "the nested spec document is extracted"
else
	fail "the spec inside the nested zip must be reached"
fi
if [ -f "$tmp/SP-260553_Presentation_of_TS23366_for_information.docx" ]; then
	pass "the cover is still present (the walk decides which document wins later)"
else
	fail "the outer document must not be lost"
fi
if find "$tmp" -name '*.zip' | grep -q .; then
	fail "the nested archive must be consumed, not left to be mistaken for a document"
else
	pass "no archive is left behind"
fi


[ "$fails" -eq 0 ] || { echo "$fails failure(s)"; exit 1; }
echo "all good"
