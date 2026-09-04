#!/usr/bin/env bash
# Offline check: the identity guards can READ the counters they gate on.
# Run: bash scripts/local/build-image_test.sh
#
# WHY THIS EXISTS. The guards used to extract three values with
#
#     sed -n 's/^embedding_model=//p'
#
# and Git Bash rewrites an argument shaped like NAME=/posix/path into a Windows
# path on its way to a native .exe. That script matches the shape, so w64devkit's
# sed.exe got a mangled program and answered "sed: unmatched '/'". DB_ID then came
# back EMPTY, and the guard below it refuses an empty identity BY DESIGN — an
# absent identity is not a matching one. So a 25 GB build died claiming the corpus
# stated no embedding_model, on a corpus stamped 38067f8c6efe, seconds after every
# data gate had passed.
#
# It only bites when a native sed is first on PATH, which is what
# scripts/local/toolchain-env.sh arranges — so the block passed when it was driven
# standalone and failed when the pipeline ran it. A test that sources the REAL
# function out of the REAL script is the only kind that would have caught that.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/build-image.sh"
fails=0
pass() { echo "PASS  $1"; }
fail() { echo "FAIL  $1"; fails=$((fails + 1)); }

[ -r "$SCRIPT" ] || { echo "FAIL  cannot read $SCRIPT"; exit 1; }

# Source the function from the script itself rather than restating it here: a copy
# would keep passing after the original was changed back.
fn=""
inside=0
while IFS= read -r line; do
	case "$line" in
		"field() {") inside=1 ;;
	esac
	[ "$inside" = 1 ] && fn="$fn$line
"
	[ "$inside" = 1 ] && [ "$line" = "}" ] && break
done <"$SCRIPT"
case "$fn" in
	*"field() {"*) : ;;
	*) echo "FAIL  no field() in build-image.sh — the guards are extracting values some other way"; exit 1 ;;
esac
eval "$fn"

counters="$(printf 'spec_versions=20163\nclauses_with_vectors=2207233\nembedding_model=38067f8c6efe\nclauses_with_sparse=194072090\nsparse_model=b13103bce7ae\n')"

for want in "embedding_model 38067f8c6efe" "sparse_model b13103bce7ae" "clauses_with_sparse 194072090"; do
	k="${want%% *}"; v="${want##* }"
	got="$(field "$k" "$counters")"
	if [ "$got" = "$v" ]; then pass "field $k = $v"; else fail "field $k = '$got', want '$v'"; fi
done

# An ABSENT key must come back empty, not partially matched. The guards turn an
# empty value into a refusal, so a helper that invented one would defeat them just
# as thoroughly as one that could not read at all.
got="$(field embedding "$counters")"
if [ -z "$got" ]; then
	pass "an absent key yields empty (embedding must not match embedding_model)"
else
	fail "field embedding = '$got', want empty — prefix matching would satisfy the guard with the wrong value"
fi

# A key whose value is empty is NOT an absent key, and the guards must be able to
# tell them apart only by the value they get.
got="$(field sparse_model "$(printf 'sparse_model=\n')")"
if [ -z "$got" ]; then
	pass "a present-but-empty value yields empty (the killed-import case the guard exists for)"
else
	fail "field sparse_model on an empty value = '$got', want empty"
fi

[ "$fails" -eq 0 ] || { echo "FAIL  $fails check(s) failed"; exit 1; }
echo "OK    the identity guards can read their counters"
