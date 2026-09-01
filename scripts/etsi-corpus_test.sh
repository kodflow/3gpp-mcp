#!/usr/bin/env bash
# Offline test for scripts/etsi-corpus.sh's portable temp-file helper.
# Run: bash scripts/etsi-corpus_test.sh
#
# Why this exists: `mktemp --suffix=.pdf` is GNU coreutils only. The Windows
# toolchain ships w64devkit's BUSYBOX mktemp, which rejects it — and because the
# call sat inside the per-deliverable loop, the very first ETSI deliverable killed
# the whole corpus-etsi step. The extension is load-bearing (convert_pdf dispatches
# on it, pdftotext refuses an unrecognised file), so it cannot simply be dropped.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/etsi-corpus.sh"
fails=0

pass() { echo "PASS  $1"; }
fail() { echo "FAIL  $1"; fails=$((fails + 1)); }

# etsi-corpus.sh runs its whole pipeline on load, so it cannot be sourced. Lift the
# helper's REAL definition out of it — testing a copy would let the two drift.
#
# Done in pure bash, with no sed/awk, on purpose. Under scripts/local/toolchain-env.sh
# the PATH's `sed` is w64devkit's ("This is not GNU sed version 4.0"), which rejects
# the /start/,/end/p range this used to rely on — a test for a GNU-vs-BusyBox bug has
# no business assuming which implementation it got.
extract_fn() { # $1 = function name
	local line def="" inside=0
	while IFS= read -r line || [ -n "$line" ]; do
		[ "$inside" -eq 0 ] && [ "$line" = "$1() {" ] && inside=1
		if [ "$inside" -eq 1 ]; then
			def="$def$line
"
			[ "$line" = "}" ] && break
		fi
	done <"$SCRIPT"
	printf '%s' "$def"
}
eval "$(extract_fn tmpfile_ext)"

if ! declare -f tmpfile_ext >/dev/null; then
	fail "tmpfile_ext could not be extracted from $SCRIPT"
	exit 1
fi

# 1. The name must actually carry the extension.
p="$(tmpfile_ext pdf)"
case "$p" in
*.pdf) pass "extension is applied (.pdf)" ;;
*) fail "expected a .pdf name, got $p" ;;
esac

# 2. The file must exist and be empty — callers redirect into it (curl -o).
if [ -f "$p" ] && [ ! -s "$p" ]; then
	pass "file exists and is empty"
else
	fail "expected an existing empty file at $p"
fi

# 3. Distinct calls must not collide: the helper is called once per deliverable,
#    and a shared name would let one download overwrite another's PDF.
q="$(tmpfile_ext pdf)"
if [ "$p" != "$q" ]; then
	pass "successive calls are distinct"
else
	fail "two calls returned the same path: $p"
fi

# 4. Any extension, not just pdf — the same helper serves the .html temp.
h="$(tmpfile_ext html)"
case "$h" in
*.html) pass "extension is not hard-coded (.html)" ;;
*) fail "expected a .html name, got $h" ;;
esac

rm -f "$p" "$q" "$h"

# 5. Regression guard: the GNU-only spelling must not come back as CODE. Comments
#    may name it — the one above does — so only non-comment lines count. This is
#    the check that would have caught the original break.
if hits="$(grep -nE '^[[:space:]]*[^#]*mktemp[^#]*--suffix' "$SCRIPT")"; then
	fail "mktemp --suffix is GNU-only and breaks on BusyBox: $hits"
else
	pass "no GNU-only mktemp --suffix in $SCRIPT"
fi

# 6. A LOST RENAME MUST BE RETRIED, NOT FATAL.
#
#    Measured 2026-09-01 with four shards crawling into one temp directory:
#    "mv: can't rename '…/Temp/tmp.a07236': Permission denied". A Windows file
#    lock holds the new file for a moment; under `set -e` that one lost rename
#    ended the shard after 183 of 2 955 deliverables, and it sat dead for an hour
#    while the other three ran on — because a dead shard and a slow one look
#    identical from the outside.
#
#    The lock is transient, so the helper retries. Proving that needs the rename
#    to FAIL first, which a real temp directory will not do on demand: a shell
#    function named `mv` is found before the PATH, so it can fail exactly twice
#    and then hand over to the real one. Testing the property, not the weather.
mv_count_file="$(command mktemp)"
echo 0 >"$mv_count_file"
mv() {
	local n
	n=$(($(cat "$mv_count_file") + 1))
	echo "$n" >"$mv_count_file"
	if [ "$n" -le 2 ]; then
		echo "mv: can't rename '$1': Permission denied" >&2
		return 1
	fi
	command mv "$@"
}
export mv_count_file
if p6="$(tmpfile_ext pdf)" && [ -f "$p6" ]; then
	attempts="$(cat "$mv_count_file")"
	if [ "$attempts" -ge 3 ]; then
		pass "a lost rename is retried ($attempts attempts) instead of ending the run"
	else
		fail "the rename succeeded in $attempts attempt(s) — the stub did not take effect"
	fi
else
	fail "tmpfile_ext gave up after a transient rename failure — one file lock ends the whole crawl"
fi
unset -f mv
rm -f "$mv_count_file"

[ "$fails" -eq 0 ] || { echo "$fails failure(s)"; exit 1; }
echo "all good"
