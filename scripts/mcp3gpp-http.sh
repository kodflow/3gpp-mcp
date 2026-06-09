#!/usr/bin/env bash
#
# mcp3gpp-http.sh TOOL JSON_ARGS — deterministic single-shot query of the local
# mcp-3gpp Streamable-HTTP server. Prints the tool's inner JSON text result.
#
# WHY HTTP (not `docker run … serve` stdio): the stdio path closes stdin (EOF) to
# end a request, which races the server's flush and intermittently returns empty.
# A persistent HTTP server has no per-request stdin lifecycle, so parallel workflow
# agents can hammer it concurrently against ONE read-only DB lock. Pure Go server +
# bash/curl client — no Python (project is Go-only, CLAUDE.md §13).
#
# Prereq: ./bin/mcp-3gpp serve -http 127.0.0.1:8765 -db data/3gpp.duckdb -no-update &
# Usage:  scripts/mcp3gpp-http.sh search_spec '{"query":"…","spec_id":"33.108","release":"Rel-17","top_k":5,"mode":"lexical"}'
#
# NOTE: deliberately NOT `set -e`/`pipefail` — a benign non-zero in the response
# pipeline must not blank the output (that bug cost a debugging round).
set -u

EP="${MCP3GPP_EP:-http://127.0.0.1:8765/mcp}"
CT='Content-Type: application/json'
ACCEPT='Accept: application/json, text/event-stream'
tool="${1:?usage: mcp3gpp-http.sh TOOL JSON_ARGS}"
# NOT `${2:-{}}` — bash parses that as `${2:-{}` + literal `}`, appending a stray
# brace to the args and producing malformed JSON (the bug that blanked every call).
args='{}'; [ "$#" -ge 2 ] && args="$2"

# A fresh session per call races server-side: the tool call can land before the
# session finished initializing → empty response (the same intermittent-empty class
# the docker-stdio path showed). So do init → notif → call as ONE unit and RETRY
# the unit until the call returns a non-empty JSON-RPC body (bounded).
out=""
for attempt in 1 2 3 4 5; do
  # 1) initialize → session id from headers (Streamable HTTP is session-scoped).
  sid="$(curl -s -D - -o /dev/null -H "$CT" -H "$ACCEPT" \
    -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"mcp3gpp-http","version":"1"}}}' \
    "$EP" | awk 'tolower($1)=="mcp-session-id:"{print $2}' | tr -d '\r')"
  [ -n "$sid" ] || { sleep 1; continue; }
  # 2) mandatory initialized notification (server returns 202).
  curl -s -o /dev/null -H "$CT" -H "$ACCEPT" -H "Mcp-Session-Id: $sid" \
    -d '{"jsonrpc":"2.0","method":"notifications/initialized"}' "$EP"
  # 3) tool call — capture whole response, then unwrap (plain JSON or SSE data: line).
  resp="$(curl -s -H "$CT" -H "$ACCEPT" -H "Mcp-Session-Id: $sid" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"$tool\",\"arguments\":$args}}" \
    "$EP")"
  out="$(printf '%s\n' "$resp" | sed 's/^data: //' | grep -E '^\{' | jq -r 'select(.id==2) | (.result.content[0].text // (.error|tostring))' 2>/dev/null)"
  [ -n "$out" ] && { printf '%s\n' "$out"; exit 0; }
  sleep 1
done
echo '{"error":"empty after 5 attempts"}'; exit 1
