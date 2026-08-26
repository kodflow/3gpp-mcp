#!/usr/bin/env bash
# snapshot-smoke.sh — prove that what a CONSUMER receives actually works.
#
# The pipeline verifies the producer thoroughly: `goal run` validates the corpus
# it built and `stepSmoke` starts the real server against the local DB. None of
# that exercises the path an actual user takes — download the published artefact
# into an empty directory and serve it — and that path is where the damage has
# historically been:
#
#   * the bootstrap URL 404'd for months (`/releases/latest/download/` is a
#     GitHub ALIAS resolving to the newest non-prerelease, not the `latest` tag),
#     and the workaround was to bake the DB into the image rather than fix two
#     constants. A local build could not have noticed;
#   * the served binary disabled vector search on every valid corpus, and the only
#     trace was one line on stderr.
#
# Both are invisible to a producer-side check by construction. This script is the
# consumer-side one.
#
#   scripts/snapshot-smoke.sh [--keep] [--dir DIR]
#
# Exit 0 only if: the artefact downloads, its digests match the published
# manifest (when one exists), the server starts, a search returns citations, and
# vector search is ENABLED.
set -uo pipefail

BASE="https://github.com/kodflow/3gpp-mcp/releases/download/latest"
KEEP=0
DIR=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --keep) KEEP=1; shift;;
    --dir)  DIR="$2"; shift 2;;
    -h|--help) sed -n '2,26p' "$0"; exit 0;;
    *) echo "unknown arg: $1" >&2; exit 2;;
  esac
done

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIR="${DIR:-$(mktemp -d -t 3gpp-snapshot-XXXXXX)}"
mkdir -p "$DIR"
log() { printf '%s | %s\n' "$(date -u +%H:%M:%S)" "$*"; }
fail() { printf '\033[1;31mSNAPSHOT SMOKE FAILED\033[0m: %s\n' "$*" >&2; exit 1; }
cleanup() {
  [[ $KEEP -eq 1 ]] && { log "kept: $DIR"; return; }
  rm -rf "$DIR"
}
trap cleanup EXIT

log "fresh directory: $DIR"

# --- 1. the artefact, from the URL a user's binary actually uses ---------------
# Not a hand-written URL: read it out of the shipped source so a constant that
# drifts breaks this check instead of silently breaking users.
DB_URL="$(grep -oE 'https://github.com/[^"]*3gpp\.duckdb\.zst' "$ROOT/cmd/server/bootstrap.go" | head -1)"
[[ -n "$DB_URL" ]] || fail "could not read the DB URL out of cmd/server/bootstrap.go"
log "bootstrap URL as compiled in: $DB_URL"

# A redirect to a release that lacks the asset is the exact F03 failure, and curl
# reports it as a plain 404 only if we follow redirects and check the final code.
code="$(curl -sIL -o /dev/null -w '%{http_code}' --max-time 60 "$DB_URL")"
[[ "$code" == "200" ]] || fail "the compiled-in bootstrap URL answers HTTP $code (F03 regression?)"

log "downloading the snapshot (~670 MB)"
curl -fL --retry 3 --max-time 1800 -o "$DIR/3gpp.duckdb.zst" "$DB_URL" || fail "download failed"

# --- 2. digests, from the manifest when it exists ------------------------------
if curl -fsSL --max-time 60 -o "$DIR/corpus-manifest.json" "$BASE/corpus-manifest.json" 2>/dev/null; then
  log "manifest found — verifying the artefact against it"
  want="$(grep -oE '"sha256"[[:space:]]*:[[:space:]]*"[a-f0-9]+"' "$DIR/corpus-manifest.json" | head -1 | grep -oE '[a-f0-9]{64}')"
  got="$(sha256sum "$DIR/3gpp.duckdb.zst" | cut -d' ' -f1)"
  [[ "$want" == "$got" ]] || fail "manifest digest $want != downloaded $got"
  log "digest OK"
else
  log "WARNING: no published corpus-manifest.json — the artefact is UNVERIFIED"
fi

log "decompressing (~6.5 GB)"
zstd -d --long=27 -f "$DIR/3gpp.duckdb.zst" -o "$DIR/3gpp.duckdb" || fail "decompression failed"
rm -f "$DIR/3gpp.duckdb.zst"

# --- 3. serve it, the way a client does ----------------------------------------
SERVER="$ROOT/.local/bin/server"
[[ -x "$SERVER" ]] || SERVER="$ROOT/.local/bin/server.exe"
[[ -x "$SERVER" ]] || fail "no server binary at .local/bin/server — run 'make goal ARGS=\"--only build-go\"' first"

log "starting the server against the DOWNLOADED corpus"
req='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"snapshot-smoke","version":"1"}}}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_spec","arguments":{"query":"AMF registration procedure","top_k":3}}}'

out="$(printf '%s\n' "$req" | "$SERVER" serve --db "$DIR/3gpp.duckdb" --no-update 2>"$DIR/stderr.log")"
rc=$?

# The server REFUSES to start on an embedding-model mismatch now, so a non-zero
# exit here is a real verdict rather than something to paper over.
if [[ $rc -ne 0 ]]; then
  log "server stderr:"; sed 's/^/    /' "$DIR/stderr.log" | tail -20
  fail "the server would not serve the published corpus (exit $rc)"
fi

grep -q '"citations"' <<<"$out" || {
  log "server stderr:"; sed 's/^/    /' "$DIR/stderr.log" | tail -20
  fail "search_spec returned no citations — the corpus serves nothing"
}
log "search returned citations"

# --- 4. vector search must be ON -----------------------------------------------
# The failure that shipped for months was silent degradation to lexical. The
# absence of an error is not evidence; the absence of the disable message is.
if grep -qi 'semantic disabled' "$DIR/stderr.log"; then
  log "server stderr:"; sed 's/^/    /' "$DIR/stderr.log" | tail -20
  fail "vector search was DISABLED on the published corpus"
fi
log "vector search enabled"

printf '\033[1;32mSNAPSHOT SMOKE PASSED\033[0m — the published artefact downloads, verifies, serves and answers with vectors.\n'
