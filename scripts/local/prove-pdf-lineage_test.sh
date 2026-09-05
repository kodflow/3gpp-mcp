#!/usr/bin/env bash
# Offline check: the PDF round-trip assertions can actually FAIL.
# Run: bash scripts/local/prove-pdf-lineage_test.sh
#
# WHY THIS EXISTS. assert-pdf-lineage.sh reports twenty OK lines on a good run,
# and a harness that only ever prints OK is indistinguishable from one whose
# greps match nothing. Every check in it was falsified by hand once, on
# 2026-09-05, against doctored copies of a real transcript — and a falsification
# done by hand once decays the moment someone edits a pattern.
#
# So this pins it. For each doctoring the test demands TWO things:
#
#   - the run fails, and
#   - EXACTLY ONE check fails, and it is the expected one.
#
# The second half is the point. A doctoring that trips three checks proves
# nothing about which check was watching: it would still "fail" if the axis
# assertion had been deleted. Counting the failures is what ties one defect to
# one guard.
#
# The fixture is synthetic and tiny on purpose. A real transcript is 138 kB of
# spec text, which is a poor thing to commit and a worse thing to keep in step
# with the assertions; what these checks read is a handful of escaped-JSON
# fields, and those are cheap to state exactly.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ASSERT="$HERE/assert-pdf-lineage.sh"
fails=0
pass() { echo "PASS  $1"; }
fail() { echo "FAIL  $1"; fails=$((fails + 1)); }

[ -r "$ASSERT" ] || { echo "FAIL  cannot read $ASSERT"; exit 1; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# A minimal transcript carrying exactly the fields the assertions read. The
# quotes are escaped because the real payload is JSON nested inside a JSON
# string, and that nesting is precisely what the `.?` in the patterns absorbs.
cat >"$TMP/good.jsonl" <<'EOF'
{"jsonrpc":"2.0","id":10,"result":{"content":[{"type":"text","text":"{\"versions\": [{\"version\": \"18.4.0\"}]}"}]}}
{"jsonrpc":"2.0","id":200,"result":{"content":[{"type":"text","text":"{\"axis\": \"version\", \"axis_values\": [\"3.0.0\", \"18.4.0\"], \"paragraphs\": [{\"introduced\": \"3.2.0\", \"obsolete\": false}]}"}]}}
{"jsonrpc":"2.0","id":201,"result":{"content":[{"type":"text","text":"{\"added\": [\"a new sentence\"], \"removed\": null, \"from\": \"3.0.0\", \"to\": \"18.4.0\", \"unchanged_paragraphs\": 0}"}]}}
{"jsonrpc":"2.0","id":300,"result":{"content":[{"type":"text","text":"{\"axis\": \"release\", \"axis_values\": [\"Rel-17\", \"Rel-18\"], \"paragraphs\": [{\"introduced\": \"Rel-15\", \"obsolete\": false}]}"}]}}
{"jsonrpc":"2.0","id":301,"result":{"content":[{"type":"text","text":"{\"added\": [\"a changed sentence\"], \"removed\": [\"the old one\"], \"from\": \"Rel-17\", \"to\": \"Rel-18\", \"unchanged_paragraphs\": 17}"}]}}
EOF

cat >"$TMP/good.env" <<'EOF'
UNDER_TEST='fixture'
SPEC_PDF='ETSI TS 102 221'
SPEC_REL='23.501'
CLAUSE='5.2.3'
CLAUSE_REL='5.4.4a'
VER_NEW='18.4.0'
VER_OLD='3.0.0'
N_VERS='126'
PDF_URL='https://www.etsi.org/deliver/etsi_ts/102200_102299/102221/18.04.00_60/ts_102221v180400p.pdf'
PDF_BYTES='1347349'
CANDIDATES='8'
HITS='6'
INTRODUCED='3.2.0'
FROM_REL='Rel-17'
TO_REL='Rel-18'
EOF

# run <transcript> <facts> -> sets RC and N_FAILED and FAILED_TEXT
run() {
  local out
  out="$(bash "$ASSERT" "$1" "$2" 2>&1)"
  RC=$?
  N_FAILED="$(printf '%s\n' "$out" | grep -c '^  FAILED')"
  FAILED_TEXT="$(printf '%s\n' "$out" | grep '^  FAILED')"
}

# ---------------------------------------------------------------- the control
# Without this, every check below is satisfied by an assert script that fails on
# everything.
run "$TMP/good.jsonl" "$TMP/good.env"
if [ "$RC" -eq 0 ] && [ "$N_FAILED" -eq 0 ]; then
  pass "a clean transcript passes"
else
  fail "the clean fixture does not pass ($N_FAILED failure(s)): $FAILED_TEXT"
fi

# expect_one <label> <transcript> <facts> <regex the single failure must match>
expect_one() {
  run "$2" "$3"
  if [ "$RC" -eq 0 ]; then
    fail "$1: doctored input still passed"
  elif [ "$N_FAILED" -ne 1 ]; then
    fail "$1: expected exactly 1 failure, got $N_FAILED: $FAILED_TEXT"
  elif printf '%s' "$FAILED_TEXT" | grep -qE "$4"; then
    pass "$1"
  else
    fail "$1: wrong check fired: $FAILED_TEXT"
  fi
}

# 1. The two axes collapse into one. The server still answers, and the answer is
#    still well-formed — only the axis name betrays it.
sed 's/\\"axis\\": \\"version\\"/\\"axis\\": \\"release\\"/' \
  "$TMP/good.jsonl" >"$TMP/axis.jsonl"
expect_one "a collapsed axis is caught" "$TMP/axis.jsonl" "$TMP/good.env" \
  "NOT traced on the version axis"

# 2. The corpus stops matching the published document. The placement rate is
#    carried in the facts file, so that is where this shows up.
sed "s/^HITS=.*/HITS='2'/" "$TMP/good.env" >"$TMP/hits.env"
expect_one "a corpus that lost the PDF's text is caught" \
  "$TMP/good.jsonl" "$TMP/hits.env" "sentences were placed"

# 3. trace_clause answers "nothing changed" to everything. An unchanged diff
#    legitimately renders null, so only the absence of ANY +/- across BOTH
#    diffs is evidence.
awk '/"id":201|"id":301/ {
       gsub(/\\"added\\": \[/,   "\\\"added\\\": null")
       gsub(/\\"removed\\": \[/, "\\\"removed\\\": null")
     } { print }' "$TMP/good.jsonl" >"$TMP/nodiff.jsonl"
expect_one "a diff that never reports a change is caught" \
  "$TMP/nodiff.jsonl" "$TMP/good.env" "neither diff produced"

# 4. A JSON-RPC error anywhere in the transcript.
{ cat "$TMP/good.jsonl"; echo '{"jsonrpc":"2.0","id":9,"error":{"code":-32602,"message":"boom"}}'; } \
  >"$TMP/err.jsonl"
expect_one "a JSON-RPC error is caught" "$TMP/err.jsonl" "$TMP/good.env" \
  "carries a JSON-RPC error"

# 5. The citation stops resolving to a real document — a stub or an error page.
sed "s/^PDF_BYTES=.*/PDF_BYTES='812'/" "$TMP/good.env" >"$TMP/stub.env"
expect_one "a citation that returns a stub is caught" \
  "$TMP/good.jsonl" "$TMP/stub.env" "not a published spec"

echo
[ "$fails" -eq 0 ] || { echo "FAILED: $fails check(s)" >&2; exit 1; }
echo "OK — every assertion in assert-pdf-lineage.sh can fail, one defect at a time"
