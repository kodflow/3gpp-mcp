#!/usr/bin/env bash
#
# demo.sh — drive the 3gpp-mcp server over stdio (real MCP JSON-RPC) and
# pretty-print what each of the 8 tools returns. This is the "test it yourself"
# entry point. Requires a built binary (make build) and a DB (make ingest).
#
# Usage: scripts/demo.sh [db]      (default db: data/3gpp.duckdb)
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/bin/mcp-3gpp"
DB="${1:-$ROOT/data/3gpp.duckdb}"
OUT="$(mktemp)"
trap 'rm -f "$OUT"' EXIT

[[ -x "$BIN" ]] || { echo "build first: make build"; exit 1; }
[[ -f "$DB"  ]] || { echo "no DB at $DB — run: make ingest ARGS=\"--spec 33.128,33.127,21.905\""; exit 1; }

# JSON-RPC frames: initialize, then one tools/call per tool we showcase.
{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"demo","version":"1"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized"}'
  printf '%s\n' '{"jsonrpc":"2.0","id":10,"method":"tools/list","params":{}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"list_specs","arguments":{"series":"33","spec_type":"TS"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"list_releases","arguments":{"spec_id":"33.128"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"get_spec","arguments":{"spec_id":"33.128","clause":"6.2.2.2"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":14,"method":"tools/call","params":{"name":"search_spec","arguments":{"query":"xIRI registration event over LI_X2","spec_id":"33.128","top_k":3}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":15,"method":"tools/call","params":{"name":"resolve_term","arguments":{"term":"AMF"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":16,"method":"tools/call","params":{"name":"trace_evolution","arguments":{"entity":"MME"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":17,"method":"tools/call","params":{"name":"find_cross_references","arguments":{"spec_id":"33.128","clause":"6.2.2"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":18,"method":"tools/call","params":{"name":"get_changelog","arguments":{"spec_id":"33.128"}}}'
  sleep 4   # keep stdin open so the server flushes every response before EOF
} | timeout 60 "$BIN" serve --db "$DB" 2>/dev/null > "$OUT"

python3 - "$OUT" <<'PY'
import sys, json
labels = {10:"tools/list", 11:"list_specs(series=33)", 12:"list_releases(33.128)",
          13:"get_spec(33.128 §6.2.2.2)", 14:"search_spec(xIRI X2)", 15:"resolve_term(AMF)",
          16:"trace_evolution(MME)", 17:"find_cross_references(33.128 §6.2.2)", 18:"get_changelog(33.128)"}
def trim(o):
    if isinstance(o, dict): return {k: trim(v) for k,v in o.items()}
    if isinstance(o, list): return [trim(x) for x in o[:6]] + (["… (+%d)"%(len(o)-6)] if len(o)>6 else [])
    if isinstance(o, str) and len(o)>200: return o[:200]+"…"
    return o
for line in open(sys.argv[1], encoding="utf-8"):
    line = line.strip()
    if not line: continue
    try: msg = json.loads(line)
    except Exception: continue
    rid = msg.get("id")
    if rid not in labels: continue
    print("\n" + "="*72 + f"\n# {labels[rid]}\n" + "="*72)
    res = msg.get("result", {})
    if rid == 10:
        names = [t["name"] for t in res.get("tools", [])]
        print(f"{len(names)} tools: " + ", ".join(sorted(names))); continue
    texts = [c.get("text","") for c in res.get("content", []) if c.get("type")=="text"]
    try:
        print(json.dumps(trim(json.loads(texts[0])), indent=2, ensure_ascii=False))
    except Exception:
        print((texts[0] if texts else "")[:800])
PY
