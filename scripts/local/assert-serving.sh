#!/usr/bin/env bash
# Assertions over a captured JSON-RPC transcript. Split out of prove-serving.sh so it can
# be tested against a saved sample without starting a server.
#
# COUNTS, not presence. server_info reports the ETSI half's own fts/hnsw/sparse in
# the SAME payload, and the whole response is ONE line — so `grep -c` returns 1
# whatever happens, and a bare presence test is satisfied by either half alone.
# `grep -oE … | wc -l` is the only form that answers "how many halves say true".
#
# The pattern is `key.?": *true`: the payload is a JSON string nested in an MCP
# content block, so the quotes arrive escaped (\"fts\": true) — `.?` absorbs the
# backslash when it is there and matches when it is not. Verified against a real
# transcript: fts true x2, hnsw false x2, attached true x1.
set -uo pipefail
OUT="${1:?usage: assert.sh <transcript>}"
fail=0

need() { # need <label> <key> <value> <expected-count>
  got=$(grep -oE "$2.?\": *$3" "$OUT" | wc -l | tr -d ' ')
  if [ "$got" -ge "$4" ]; then
    printf '  OK      %-22s %s=%s x%s\n' "$1" "$2" "$3" "$got"
  else
    printf '  MISSING %-22s %s=%s got x%s want x%s\n' "$1" "$2" "$3" "$got" "$4"
    fail=$((fail + 1))
  fi
}
saw() { # saw <label> <regex>
  if grep -qE "$2" "$OUT"; then printf '  OK      %s\n' "$1"
  else printf '  MISSING %s\n' "$1"; fail=$((fail + 1)); fi
}
# absent is the mirror of saw, and the glossary is why it exists: some defects are
# a value that IS there rather than one that is missing, and a harness that can
# only look for presence cannot see them.
absent() { # absent <label> <regex> <why>
  if grep -qE "$2" "$OUT"; then printf '  PRESENT %s\n          %s\n' "$1" "$3"; fail=$((fail + 1))
  else printf '  OK      %s\n' "$1"; fi
}

echo "===== arms, counted across both corpus halves ====="
need "lexical/BM25"      fts       true 2
need "vector index"      hnsw      true 2
need "learned-lexical"   sparse    true 2
need "query embedder"    semantic  true 1
need "cross-encoder"     reranker  true 1
need "ETSI federated"    attached  true 1
need "identities agree"  embedding_model_ok true 1

echo "===== the calls answered ====="
saw "semantic search answered"   '"id":3'
saw "federated search answered"  '"id":4'
saw "trace_evolution answered"   '"id":5'
saw "resolve_term answered"      '"id":6'
saw "resolve_term (ambiguous)"   '"id":7'

echo "===== the glossary, which nothing here used to look at ====="
# UICC is defined in the ETSI deliverables and NOT in TS 21.905, so a match at all
# proves the federation reached the ETSI half — the arm `attached` only claims.
saw "the ETSI half answers UICC" 'Universal Integrated Circuit Card'
# EVERY ETSI ROW MUST NAME THE DELIVERABLE THAT DECLARES IT. The payload is a JSON
# string inside an MCP content block, so the quotes arrive escaped; `.?` absorbs
# the backslash the way `need` already does.
saw "an ETSI row cites a deliverable" 'source_series.?": *.?"ETSI (TS|EN|TR|ES|EG|SR) '
# …AND THE CONSTANT MUST BE GONE. "etsi" names no document: it can be neither
# opened by a reader nor ranked by Store.ResolveTerm, so all 4 941 rows tied and
# the tie-break — domain, expansion, with domain empty — answered by ALPHABET.
absent "no un-citable \"etsi\" provenance" 'source_series.?": *.?"etsi.?"' \
  'this corpus predates enrich-etsi: run `make build` so the glossary pass runs'

if grep -q '"error"' "$OUT"; then
  echo "  MISSING no JSON-RPC error — the transcript carries one"
  fail=$((fail + 1))
fi

[ "$fail" -eq 0 ] || { echo "ASSERT FAILED: $fail check(s)" >&2; exit 1; }
echo "ASSERT OK"
