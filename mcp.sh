#!/usr/bin/env bash
# mcp.sh <tool> '<json-args>'  -> prints the tool's inner text (JSON). Pure bash + curl + jq.
# 3GPP MCP HTTP server is the source of truth. No Python, no docker-stdio race.
EP="${MCP_EP:-http://127.0.0.1:8765}"; ACC='application/json, text/event-stream'
TOOL="${1:?tool}"; ARGS="${2}"; [ -z "$ARGS" ] && ARGS="{}"
BODY="$(jq -nc --arg t "$TOOL" --argjson a "$ARGS" \
  '{jsonrpc:"2.0",id:2,method:"tools/call",params:{name:$t,arguments:$a}}')" || { echo "ERR:bad-args" >&2; exit 2; }
HDR="$(mktemp)"
curl -s -D "$HDR" -o /dev/null -X POST "$EP/mcp" -H 'Content-Type: application/json' -H "Accept: $ACC" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"wf","version":"0"}}}'
SID="$(grep -i '^Mcp-Session-Id:' "$HDR" | tr -d '\r' | awk '{print $2}')"; rm -f "$HDR"
[ -z "$SID" ] && { echo "ERR:no-session" >&2; exit 3; }
curl -s -o /dev/null -X POST "$EP/mcp" -H 'Content-Type: application/json' -H "Accept: $ACC" -H "Mcp-Session-Id: $SID" \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'
for _try in 1 2 3; do RESP="$(curl -s -X POST "$EP/mcp" -H "Content-Type: application/json" -H "Accept: $ACC" -H "Mcp-Session-Id: $SID" -d "$BODY")"; [ -n "$RESP" ] && break; sleep 1; done
P="$(printf '%s' "$RESP" | sed -n 's/^data: //p' | head -1)"; [ -z "$P" ] && P="$RESP"
printf '%s' "$P" | jq -r '.result.content[0].text // ("MCP_ERROR:"+(.error|tostring))'
