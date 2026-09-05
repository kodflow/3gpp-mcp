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

# ---------------------------------------------------------------------------
# The corpus halves must be packed into SEPARATE layers.
#
# A registry dedupes by layer digest: a half that did not change is answered with
# "existing blob" and never crosses the wire. Both halves in one tar throws that
# away, and it throws it away SILENTLY — the publish succeeds, the image is
# correct, and the only symptom is that it took twice as long.
#
# Measured on the 2026-09-05 publish, before the split: seeding 679 glossary rows
# grew 3gpp.duckdb by 9.7 MB and left etsi.duckdb identical to the byte, and
# 19.7 GB of unchanged ETSI went up the wire regardless. Nothing failed. Nothing
# reported it. That is why this is pinned in a test rather than a comment.
#
# Read out of the real script, not restated here: a copy would keep passing after
# the original was changed back.
corpus_layers="$(grep -E '^\[ "\$WITH_CORPUS" = 1 \] && layer ' "$SCRIPT")"
if [ -z "$corpus_layers" ]; then
	fail "no corpus layer line in build-image.sh — the packing moved and this check is now blind"
else
	# PER LINE, and order-independent. The first version of this check matched
	# "3gpp.duckdb.*etsi.duckdb" on the whole block, so a single combined layer
	# written with the two paths the OTHER way round sailed through it — the
	# check depended on the order of two filenames nobody promised to keep.
	# Counting how many halves each line carries cannot be fooled that way.
	shared=0
	names=""
	while IFS= read -r ln; do
		[ -n "$ln" ] || continue
		n=0
		case "$ln" in *3gpp.duckdb*) n=$((n + 1)) ;; esac
		case "$ln" in *etsi.duckdb*) n=$((n + 1)) ;; esac
		[ "$n" -ge 2 ] && shared=$((shared + 1))
		names="$names$(printf '%s' "$ln" | awk '{for (i = 1; i < NF; i++) if ($i == "layer") { print $(i + 1); exit }}')
"
	done <<EOF
$corpus_layers
EOF

	if [ "$shared" -ne 0 ]; then
		fail "a layer carries BOTH corpus halves; an unchanged half is re-pushed with the changed one"
	else
		pass "no layer carries more than one corpus half"
	fi

	# Two layers under one name would have the second overwrite the first's tar,
	# and the image would ship a half twice and the other not at all.
	dups="$(printf '%s' "$names" | sort | uniq -d | grep -c .)"
	if [ "$dups" -ne 0 ]; then
		fail "two corpus layers share a name; the second would overwrite the first's tar"
	else
		pass "each corpus layer has its own name"
	fi

	# And each half must actually be packed, or the image ships without it.
	# "split them" and "drop one" look identical from a distance.
	for half in 3gpp etsi; do
		if printf '%s
' "$corpus_layers" | grep -q "$half\.duckdb"; then
			pass "$half.duckdb is packed"
		else
			fail "$half.duckdb is in no layer — the image would ship without that half"
		fi
	done
fi

# ---------------------------------------------------------------------------
# stage() puts the corpus in the rootfs without copying 42.8 GB, and the whole
# reason that is acceptable is that deleting the staged name must never touch the
# original. Sourced out of the real script, like field() above.
stage_fn=""
inside=0
while IFS= read -r line; do
	case "$line" in
		"stage() {") inside=1 ;;
	esac
	[ "$inside" = 1 ] && stage_fn="$stage_fn$line
"
	[ "$inside" = 1 ] && [ "$line" = "}" ] && break
done <"$SCRIPT"
case "$stage_fn" in
	*"stage() {"*) eval "$stage_fn" ;;
	*) fail "no stage() in build-image.sh — the corpus is being staged some other way" ;;
esac

if command -v stage >/dev/null 2>&1 || [ -n "$stage_fn" ]; then
	tmp="$(mktemp -d)"
	printf 'corpus bytes\n' >"$tmp/src"

	stage "$tmp/src" "$tmp/dst"
	if [ -f "$tmp/dst" ] && [ "$(cat "$tmp/dst")" = "corpus bytes" ]; then
		pass "stage puts the content at the destination"
	else
		fail "stage did not produce the content at the destination"
	fi

	# THE SAFETY PROPERTY. rm -rf "$STAGE" runs at the top of every build, and it
	# runs over a name that may be a hard link to the corpus. Unlinking must leave
	# the original whole; if this ever stops holding, a build deletes the corpus.
	rm -f "$tmp/dst"
	if [ -f "$tmp/src" ] && [ "$(cat "$tmp/src")" = "corpus bytes" ]; then
		pass "deleting the staged name leaves the original intact"
	else
		fail "deleting the staged name destroyed the original — stage() must never be a move"
	fi

	# Re-staging over an existing destination must replace it, not fail or append:
	# a rebuild stages into a tree a previous run may have left behind.
	printf 'new corpus\n' >"$tmp/src2"
	stage "$tmp/src" "$tmp/dst2"
	stage "$tmp/src2" "$tmp/dst2"
	if [ "$(cat "$tmp/dst2")" = "new corpus" ]; then
		pass "re-staging replaces an existing destination"
	else
		fail "re-staging left the old content at the destination"
	fi

	# And it must still work where hard links are unavailable — the fallback is
	# what keeps the build portable, so a stage() reduced to a bare ln would pass
	# every check above on THIS filesystem and fail on someone else's.
	case "$stage_fn" in
		*"cp "*) pass "stage falls back to a copy when the link cannot be made" ;;
		*) fail "stage has no copy fallback; it would break where hard links are unavailable" ;;
	esac

	rm -rf "$tmp"
fi

[ "$fails" -eq 0 ] || { echo "FAIL  $fails check(s) failed"; exit 1; }
echo "OK    the identity guards read their counters, the corpus halves ship apart, and staging never moves the corpus"
