#!/usr/bin/env bash
# Offline test for the soffice hang cache in scripts/lib/convert.sh.
# Run: bash scripts/lib/convert_test.sh
#
# Why this exists: a document soffice CRASHES on fails in seconds and falls
# through harmlessly. A document it HANGS on costs CONV_TIMEOUT twice — the
# straight attempt and the EMF-stripped retry — which is 30 minutes for one file,
# on EVERY run. TS 38.141-1 did exactly that: 900 s, then 900 s again, to end at
# the pandoc fallback it was always going to end at.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
fails=0
pass() { echo "PASS  $1"; }
fail() { echo "FAIL  $1"; fails=$((fails + 1)); }

# Point the cache at a throwaway file, then source the library. SOFFICE_TIMEOUT_LIST
# is a `: "${VAR:=default}"` assignment, so presetting it wins.
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
export SOFFICE_TIMEOUT_LIST="$WORK/soffice-timeouts.txt"
# shellcheck source=scripts/lib/convert.sh
source "$HERE/convert.sh"

# 1. The key must identify the DOCUMENT, not the throwaway path it arrived on.
if [ "$(_conv_key "/tmp/whatever/38141-1-h60.html")" = "38141-1-h60" ]; then
	pass "the key is the spec+version, not the path"
else
	fail "expected 38141-1-h60, got $(_conv_key "/tmp/whatever/38141-1-h60.html")"
fi

# 2. An unknown document must NOT be skipped — the cache only ever excuses a
#    document that was actually observed to hang.
if _soffice_known_hang "$WORK/23501-j50.html"; then
	fail "an unrecorded document must not be treated as a known hang"
else
	pass "an unrecorded document is not skipped"
fi

# 3. Recording a hang makes it known, and by identity rather than by path: the
#    next run sees the same document through a different temp directory.
_soffice_note_hang "$WORK/38141-1-h60.html"
if _soffice_known_hang "/somewhere/else/entirely/38141-1-h60.html"; then
	pass "a recorded hang is recognised from any path"
else
	fail "the cache must key on identity, not on the path it was recorded from"
fi

# 4. Recording twice must not duplicate: this file is appended to on every run.
_soffice_note_hang "$WORK/38141-1-h60.html"
n="$(grep -c . "$SOFFICE_TIMEOUT_LIST")"
if [ "$n" -eq 1 ]; then
	pass "recording is idempotent ($n line)"
else
	fail "expected 1 line, got $n — the cache would grow without bound"
fi

# 5. A neighbouring document must stay unaffected. A cache that over-matches would
#    send perfectly convertible specs to the degraded pandoc path in silence.
if _soffice_known_hang "$WORK/38141-2-h60.html"; then
	fail "38141-2 must not inherit 38141-1's verdict"
else
	pass "the cache does not over-match"
fi

# 6. Deleting the file must restore the measured behaviour — this is a cache of an
#    observation, not a verdict, and a LibreOffice upgrade is why someone clears it.
rm -f "$SOFFICE_TIMEOUT_LIST"
if _soffice_known_hang "$WORK/38141-1-h60.html"; then
	fail "clearing the cache must make the next run re-measure"
else
	pass "clearing the cache restores measurement"
fi

[ "$fails" -eq 0 ] || { echo "$fails failure(s)"; exit 1; }
echo "all good"
