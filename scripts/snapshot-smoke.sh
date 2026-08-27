#!/usr/bin/env bash
# snapshot-smoke.sh — prove that what a CONSUMER receives actually works.
#
# The pipeline verifies the producer thoroughly: `goal run` validates the corpus
# it built and `stepSmoke` starts the real server against the local DB. None of
# that exercises the path an actual user takes — `bootstrap` into an empty cache
# and serve what lands there — and that path is where the damage has
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

# --- 1. the artefact, by RUNNING the command a consumer runs -------------------
#
# This step used to grep the download URL out of cmd/server/bootstrap.go and
# curl it by hand. That is the defect class this repository keeps paying for: a
# check that RE-IMPLEMENTS what it gates eventually asks a different question
# than the code it gates, and then it passes. It also could not survive the
# corpus moving off the public release — the grep simply found nothing.
#
# So the consumer path is now exercised by running the consumer's command:
# `mcp-3gpp bootstrap` into an empty cache. Whatever bootstrap does — private
# GHCR package, credential resolution, Range-resumed layer pull, digest
# verification, tar extraction — is what gets tested, because it is what runs.
BOOTSTRAP="$ROOT/.local/bin/server"
[[ -x "$BOOTSTRAP" ]] || BOOTSTRAP="$ROOT/.local/bin/server.exe"
[[ -x "$BOOTSTRAP" ]] || fail "no binary at .local/bin/server — run 'make goal ARGS=\"--only build-go\"' first"

# The corpus package is private on purpose (DATA_NOTICE: verbatim standards
# text). Without a credential this check cannot run at all, and saying so is
# better than a red build that means "no token here".
if [[ -z "${GHCR_PAT:-}" && -z "${GITHUB_TOKEN:-}" && ! -s "$ROOT/.local/ghcr.pat" ]]; then
  log "SKIPPED: no GHCR credential (GHCR_PAT, GITHUB_TOKEN or .local/ghcr.pat)."
  log "The consumer path pulls a PRIVATE package; a token with read:packages is required."
  exit 0
fi

log "running the real consumer command: bootstrap into an empty cache"
MCP3GPP_CACHE="$DIR" "$BOOTSTRAP" bootstrap || fail "bootstrap failed — this is the path every new user takes"
[[ -s "$DIR/3gpp.duckdb" ]] || fail "bootstrap reported success but $DIR/3gpp.duckdb is absent or empty"
log "bootstrap produced $(du -h "$DIR/3gpp.duckdb" | cut -f1) at $DIR/3gpp.duckdb"

# bootstrap verifies the layer against the digest the registry manifest names, so
# there is no second hand-rolled digest check to drift from it. What is worth
# asserting here is that it recorded the identity it pulled — that sidecar is
# what a later start reads to decide whether the cache is current.
[[ -s "$DIR/3gpp.duckdb.digest" ]] || fail "bootstrap left no .digest sidecar; a later serve would re-pull the whole corpus"
log "recorded corpus identity: $(cut -c1-24 < "$DIR/3gpp.duckdb.digest")…"

# --- 2. serve it, the way a client does ----------------------------------------
SERVER="$BOOTSTRAP"

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
