#!/usr/bin/env bash
# Offline test for scripts/etsi-fetch.sh's worker pool.
# Run: bash scripts/etsi-fetch_test.sh
#
# The download loop used to be sequential: one curl, one pdftotext, then the next,
# for as many as 7 501 deliverables. Turning it into an xargs pool introduces three
# failure modes that a sequential loop could not have, and every one of them is
# SILENT — the run finishes, the log looks plausible, and the corpus is short:
#
#   1. An ETSI id CONTAINS A SPACE ("103 221-1"). Splitting the work list on
#      whitespace hands xargs two arguments for one deliverable, so the URL fetched
#      is not the URL discovered and the id is truncated.
#   2. Counters are per-process. `ok=$((ok+1))` inside a worker is invisible to the
#      parent, so "converted=N failed=M" would report 0 0 forever.
#   3. A worker killed between writing the provenance header and the body leaves a
#      header-only file that the resume check counts as done.
#
# Nothing here touches the network: curl and convert_pdf are stubbed.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
fails=0
pass() { echo "PASS  $1"; }
fail() { echo "FAIL  $1"; fails=$((fails + 1)); }

# extract_fn — lift a function's REAL definition out of a script, so the test
# cannot drift from the shipped code. Pure bash: under the Windows toolchain the
# PATH's sed is w64devkit's and rejects the ranges GNU sed accepts.
extract_fn() { # $1 = name, $2 = file
	local line def="" inside=0
	while IFS= read -r line || [ -n "$line" ]; do
		[ "$inside" -eq 0 ] && [ "$line" = "$1() {" ] && inside=1
		if [ "$inside" -eq 1 ]; then
			def="$def$line
"
			[ "$line" = "}" ] && break
		fi
	done <"$2"
	printf '%s' "$def"
}

FETCH_ONE="$(extract_fn fetch_one "$HERE/etsi-fetch.sh")"
if [ -z "$FETCH_ONE" ]; then
	fail "fetch_one could not be extracted from $HERE/etsi-fetch.sh"
	exit 1
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
export BUCKET="$WORK/bucket"
export TALLY="$WORK/tally"
mkdir -p "$BUCKET" "$TALLY/ok" "$TALLY/fail"

# --- stubs -------------------------------------------------------------------
# curl records the URL it was asked for and writes a fake PDF.
curl() {
	local out="" url=""
	while [ $# -gt 0 ]; do
		case "$1" in
		-o) out="$2"; shift 2;;
		-*) shift;;
		*) url="$1"; shift;;
		esac
	done
	printf '%s\n' "$url" >>"$WORK/urls"
	printf 'PDF' >"$out"
}
retry() { "$@"; }
tmpfile_ext() { local f; f="$(mktemp)"; mv "$f" "$f.$1"; printf '%s\n' "$f.$1"; }
convert_pdf() { printf 'BODY\n' >"$2"; }
export -f curl retry tmpfile_ext convert_pdf

eval "$FETCH_ONE"

# --- 1. an id with a space survives ------------------------------------------
# This is the real TS 103 221-1, the ETSI Lawful-Interception internal interface.
fetch_one "$(printf '103 221-1\thttps://example.invalid/ts_103221-1v010401p.pdf\t1.4.1\tTS')"

want="$BUCKET/TS_103_221-1_v1.4.1.html"
if [ -s "$want" ]; then
	pass "an id containing a space produces one correctly named file"
else
	fail "an id containing a space did not produce $want (got: $(ls "$BUCKET" 2>/dev/null | tr '\n' ' '))"
fi

if [ "$(cat "$WORK/urls" 2>/dev/null)" = "https://example.invalid/ts_103221-1v010401p.pdf" ]; then
	pass "the URL fetched is the URL the work list gave"
else
	fail "the URL was mangled: $(cat "$WORK/urls" 2>/dev/null)"
fi

# The provenance header htmlparse keys on must carry the id WITH its space.
if head -1 "$want" | grep -q '<!-- ETSI-SPEC: 103 221-1 | 1.4.1 | TS -->'; then
	pass "the provenance header keeps the id's space"
else
	fail "the provenance header is wrong: $(head -1 "$want")"
fi

# --- 2. the tally is files, not variables ------------------------------------
if [ "$(find "$TALLY/ok" -type f | wc -l | tr -dc '0-9')" = "1" ]; then
	pass "a success is recorded where the parent can count it"
else
	fail "the success tally is empty — the parent would report converted=0"
fi

# A second call on the same deliverable resumes and still counts.
fetch_one "$(printf '103 221-1\thttps://example.invalid/ts_103221-1v010401p.pdf\t1.4.1\tTS')"
if [ "$(find "$TALLY/ok" -type f | wc -l | tr -dc '0-9')" = "2" ]; then
	pass "a resumed deliverable counts as converted, not as failed"
else
	fail "the resume path did not record its outcome"
fi

# --- 3. a TR and a TS with the same number do not collide --------------------
fetch_one "$(printf '103 101\thttps://example.invalid/tr_103101.pdf\t1.1.1\tTR')"
if [ -s "$BUCKET/TR_103_101_v1.1.1.html" ]; then
	pass "the document type is part of the filename (TR 103 101 vs the TS tree)"
else
	fail "the TR did not get a type-prefixed name"
fi

# --- 4. nothing is published half-written ------------------------------------
# The body is written to <target>.part and MOVED into place, so a kill between the
# header and the body cannot leave a header-only file that resume counts as done.
if grep -q 'BODY' "$want" && ! ls "$BUCKET"/*.part >/dev/null 2>&1; then
	pass "the converted file is published by a rename, leaving no .part behind"
else
	fail "a .part file survived, or the body never made it into the target"
fi

# --- 5. a failed convert is counted as a failure -----------------------------
convert_pdf() { return 1; }
export -f convert_pdf
fetch_one "$(printf '199 999\thttps://example.invalid/ts_199999.pdf\t9.9.9\tTS')" >/dev/null 2>&1
if [ "$(find "$TALLY/fail" -type f | wc -l | tr -dc '0-9')" = "1" ]; then
	pass "a deliverable with no text layer is recorded as failed, not silently skipped"
else
	fail "a failed convert left no trace — converted=N failed=M would understate the loss"
fi

# --- 6. THE POOL ITSELF, not just the worker ---------------------------------
#
# Everything above calls fetch_one directly with a whole line, which is exactly
# what a whitespace-splitting xargs would NOT do. The bug those tests cannot see
# lives in the dispatch line, so run the real one: the work list goes through the
# same `tr | xargs -0 -P -n1` the script uses, with an id that contains a space.
#
# Falsified while writing this, and measured rather than assumed. Replacing the
# NUL-delimited pipeline with a plain `xargs -P "$JOBS" -n1` turns ONE work-list
# line into FIVE worker calls:
#
#     recu: [103]
#     recu: [221-1]
#     recu: [https://example.invalid/a.pdf]
#     recu: [1.4.1]
#     recu: [TS]
#
# Every field becomes its own "deliverable", so nothing is fetched and nothing
# says so — the tally would read converted=0 failed=0 over an empty work list.
# `|| true` is load-bearing under `set -e`: without it a grep that finds nothing
# kills this script on the spot, and the assertion below never runs. Caught by
# falsifying the dispatch and watching the test exit 1 while printing NOTHING —
# a test that fails silently is worth less than no test, because it is trusted.
DISPATCH="$(grep -n "xargs -0 -P" "$HERE/etsi-fetch.sh" | head -1 | cut -d: -f2- || true)"
if [ -z "$DISPATCH" ]; then
	fail "the NUL-delimited dispatch line is gone from etsi-fetch.sh — a whitespace-splitting
      xargs turns the id '103 221-1' into two deliverables and fetches neither"
	echo "$fails failure(s)"
	exit "$fails"
fi
pass "the dispatch is NUL-delimited"

rm -rf "$BUCKET" "$TALLY" "$WORK/urls"
mkdir -p "$BUCKET" "$TALLY/ok" "$TALLY/fail"
convert_pdf() { printf 'BODY\n' >"$2"; }
export -f convert_pdf fetch_one
export WORK

wl="$WORK/wl.tsv"
{
	printf '103 221-1\thttps://example.invalid/a.pdf\t1.4.1\tTS\n'
	printf '103 101\thttps://example.invalid/b.pdf\t1.1.1\tTR\n'
	printf '102 232-1\thttps://example.invalid/c.pdf\t3.1.1\tTS\n'
} >"$wl"

JOBS=4
eval "$DISPATCH" <"$wl" || true

n=$(find "$BUCKET" -name '*.html' -type f | wc -l | tr -dc '0-9')
if [ "${n:-0}" = "3" ]; then
	pass "the pool produced one file per deliverable (3), ids with spaces included"
else
	fail "the pool produced ${n:-0} file(s) for 3 deliverables: $(ls "$BUCKET" | tr '\n' ' ')"
fi

if [ "$(sort -u "$WORK/urls" 2>/dev/null | wc -l | tr -dc '0-9')" = "3" ]; then
	pass "the pool fetched exactly the 3 URLs the work list named"
else
	fail "the pool fetched the wrong URL set: $(sort -u "$WORK/urls" 2>/dev/null | tr '\n' ' ')"
fi

[ "$fails" -eq 0 ] && echo "all good" || echo "$fails failure(s)"
exit "$fails"
