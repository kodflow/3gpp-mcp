#!/usr/bin/env bash
# Offline check: scripts/demo.sh exercises EVERY tool the server registers.
# Run: bash scripts/demo_test.sh
#
# Why this exists. `make demo` is documented as "show every tool's output" and is
# what the README sends a newcomer to. It called eight of twelve, and the four it
# skipped were help and server_info (the two entry points a client is told to call
# first), trace_clause (the tool that answers "what changed") and search_api (the
# only one reaching the OpenAPI side). A demo that shows everything except the
# tools someone would reach for is not a demo of the product, and nothing said so.
#
# The list is READ FROM THE SOURCE, never written here: a hand-kept copy drifts the
# first time a tool is added, which is exactly the failure being fixed.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(dirname "$HERE")"
fails=0
pass() { echo "PASS  $1"; }
fail() { echo "FAIL  $1"; fails=$((fails + 1)); }

server="$ROOT/internal/mcp/server.go"
demo="$ROOT/scripts/demo.sh"
for f in "$server" "$demo"; do
	[ -f "$f" ] || { echo "SKIP  $f is missing — cannot check"; exit 0; }
done

registered="$(grep -o 'mcp.NewTool("[a-z_]*"' "$server" | sed 's/.*("//; s/"//' | sort -u)"
[ -n "$registered" ] || { fail "no tool found in $(basename "$server") — the extraction broke, not the demo"; exit 1; }

called="$(grep -oE '"name":"[a-z_]+"' "$demo" | sed 's/.*:"//; s/"//' | sort -u)"

missing=""
for t in $registered; do
	case " $(echo $called) " in
	*" $t "*) ;;
	*) missing="$missing $t" ;;
	esac
done

if [ -n "$missing" ]; then
	fail "demo.sh never calls:$missing"
else
	pass "demo.sh calls every registered tool ($(echo "$registered" | wc -w | tr -d ' '))"
fi

# The reverse direction matters too: a call left behind after a tool is renamed
# prints an empty panel and looks like a broken server rather than a stale script.
stale=""
for t in $called; do
	case " $(echo $registered) " in
	*" $t "*) ;;
	*) stale="$stale $t" ;;
	esac
done
# "demo" is the clientInfo name in the initialize frame, not a tool.
stale="$(echo $stale | sed 's/\bdemo\b//g' | tr -s ' ')"
if [ -n "$(echo "$stale" | tr -d ' ')" ]; then
	fail "demo.sh calls tools the server does not register:$stale"
else
	pass "demo.sh calls no tool the server does not register"
fi

[ "$fails" -eq 0 ] || exit 1
