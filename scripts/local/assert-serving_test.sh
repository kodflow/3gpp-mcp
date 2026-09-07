#!/usr/bin/env bash
# Offline harness for assert-serving.sh — no server, no corpus, no network.
#
# assert-serving.sh was split out of prove-serving.sh with this in its own header:
# "so it can be tested against a saved sample without starting a server". Nothing
# ever tested it. That matters more than it sounds: this script is the only thing
# standing between a degraded server and a green PROVE, and an assertion that
# silently matches nothing looks exactly like an assertion that passes.
#
# The escaping is the specific trap. The MCP payload is a JSON string nested in a
# content block, so `"source_series": "etsi"` arrives as \"source_series\": \"etsi\"
# — a pattern written against the unescaped form matches nothing, for ever, while
# printing OK.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ASSERT="$HERE/assert-serving.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
rc=0

check() { # check <name> <expected-status: pass|fail> <transcript-file>
  local name=$1 want=$2 file=$3 out status
  out=$(bash "$ASSERT" "$file" 2>&1)
  status=$?
  if [ "$want" = pass ] && [ "$status" -ne 0 ]; then
    printf 'FAIL %s: expected the assertions to pass, got exit %d\n%s\n' "$name" "$status" "$out"
    rc=1
  elif [ "$want" = fail ] && [ "$status" -eq 0 ]; then
    printf 'FAIL %s: expected the assertions to FAIL, they passed\n%s\n' "$name" "$out"
    rc=1
  else
    printf 'ok   %s\n' "$name"
  fi
}

# A transcript that satisfies every arm. The escaped quotes are deliberate: this
# is the shape the server really emits.
good() {
  cat <<'JSON'
{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"{\"fts\": true, \"hnsw\": true, \"sparse\": true, \"semantic\": true, \"reranker\": true, \"attached\": true, \"embedding_model_ok\": true, \"etsi\": {\"fts\": true, \"hnsw\": true, \"sparse\": true}}"}]}}
{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"{}"}]}}
{"jsonrpc":"2.0","id":4,"result":{"content":[{"type":"text","text":"{}"}]}}
{"jsonrpc":"2.0","id":5,"result":{"content":[{"type":"text","text":"{}"}]}}
{"jsonrpc":"2.0","id":6,"result":{"content":[{"type":"text","text":"{\"term\": \"UICC\", \"matches\": [{\"expansion\": \"Universal Integrated Circuit Card\", \"source_series\": \"ETSI TS 102 221\", \"declared_by\": 41}]}"}]}}
{"jsonrpc":"2.0","id":7,"result":{"content":[{"type":"text","text":"{\"term\": \"MSC\", \"matches\": [{\"expansion\": \"Mobile-services Switching Centre\", \"source_series\": \"ETSI TS 101 200\", \"declared_by\": 37}]}"}]}}
JSON
}

good >"$TMP/good.jsonl"
check "a healthy transcript passes" pass "$TMP/good.jsonl"

# THE DEFECT ITSELF: the provenance every ETSI row carried until enrich-etsi ran.
# "etsi" names no document, so the row cannot be opened by a reader nor ranked by
# Store.ResolveTerm — which is what left the ETSI half answering alphabetically.
good | sed 's/ETSI TS 102 221/etsi/; s/ETSI TS 101 200/etsi/' >"$TMP/uncitable.jsonl"
check "the un-citable \"etsi\" provenance is caught" fail "$TMP/uncitable.jsonl"

# The federation silently degraded to 3GPP-only: `attached` can still be true
# while the ETSI half contributes nothing, so the arm has to be probed by a term
# only ETSI defines.
good | grep -v '"id":6' >"$TMP/no-etsi-answer.jsonl"
check "a missing ETSI glossary answer is caught" fail "$TMP/no-etsi-answer.jsonl"

# The regression that started all of this: one half of the pair reporting an arm
# while the other does not. `grep -c` would return 1 and call it proven.
good | sed 's/\\"sparse\\": true, \\"semantic/\\"sparse\\": false, \\"semantic/' >"$TMP/one-half.jsonl"
check "an arm live on only one half is caught" fail "$TMP/one-half.jsonl"

# A JSON-RPC error anywhere fails the run, however green the arms look.
{ good; echo '{"jsonrpc":"2.0","id":8,"error":{"code":-32603,"message":"boom"}}'; } >"$TMP/err.jsonl"
check "a JSON-RPC error is caught" fail "$TMP/err.jsonl"

[ "$rc" -eq 0 ] && echo "assert-serving_test: OK"
exit "$rc"
