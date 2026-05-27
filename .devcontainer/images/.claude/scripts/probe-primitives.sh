#!/usr/bin/env bash
# probe-primitives.sh — Skills Architecture v1.3 (PR0)
#
# WHY: Each skill in the modernization initiative consumes Claude Code
# primitives whose presence cannot be assumed across environments. This
# probe records availability at session start so downstream skills can
# choose primitive vs. documented fallback deterministically.
#
# Emits .claude/state/primitives.json with three statuses per primitive:
#   present | absent | unknown
# `unknown` means the source-of-truth (tool schema or MCP config) was
# unavailable; it never hard-fails this probe — only the consuming skill
# decides whether to escalate.

set -euo pipefail

SCHEMA="${CLAUDE_CODE_TOOL_SCHEMA_PATH:-}"
MCP_CONFIG=".mcp.json"
OUTPUT=".claude/state/primitives.json"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --schema)      SCHEMA="$2"; shift 2 ;;
    --mcp-config)  MCP_CONFIG="$2"; shift 2 ;;
    --output)      OUTPUT="$2"; shift 2 ;;
    -h|--help)     sed -n '2,16p' "$0"; exit 0 ;;
    *)             echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

mkdir -p "$(dirname "$OUTPUT")"

# Fall back to `claude --print-tool-schema` if schema path unset
if [[ -z "$SCHEMA" || ! -r "$SCHEMA" ]]; then
  if command -v claude >/dev/null 2>&1; then
    TMP_SCHEMA="$(mktemp)"
    if claude --print-tool-schema >"$TMP_SCHEMA" 2>/dev/null; then
      SCHEMA="$TMP_SCHEMA"
    else
      SCHEMA=""
    fi
  else
    SCHEMA=""
  fi
fi

probe_tool() {
  # WHY: jq -e returns non-zero when the filter yields null/false, which is
  # exactly the absent signal we want. The outer if-else keeps the script
  # under `set -e` without spurious aborts.
  local name="$1"
  if [[ -z "$SCHEMA" || ! -r "$SCHEMA" ]]; then
    echo "unknown"; return
  fi
  if jq -e --arg n "$name" '.tools[]? | select(.name==$n)' "$SCHEMA" >/dev/null 2>&1; then
    echo "present"
  else
    echo "absent"
  fi
}

probe_tool_property() {
  local tool="$1" prop="$2"
  if [[ -z "$SCHEMA" || ! -r "$SCHEMA" ]]; then
    echo "unknown"; return
  fi
  if jq -e --arg t "$tool" --arg p "$prop" \
       '.tools[]? | select(.name==$t) | .input_schema.properties[$p]' \
       "$SCHEMA" >/dev/null 2>&1; then
    echo "present"
  else
    echo "absent"
  fi
}

probe_mcp() {
  local server="$1"
  for cfg in "$MCP_CONFIG" "$HOME/.claude/.mcp.json"; do
    [[ -r "$cfg" ]] || continue
    if jq -e --arg s "$server" '.mcpServers[$s]' "$cfg" >/dev/null 2>&1; then
      echo "present"; return
    fi
  done
  echo "absent"
}

exitplanmode_schema() {
  # WHY: PR1 must reject if ExitPlanMode.input_schema.properties.plan is not
  # a string property. Record the schema here so PR1 can grep it without
  # re-fetching. We default to `null` whenever the schema is unreachable or
  # the tool is absent — `--argjson null` is a valid jq input.
  local raw
  if [[ -z "$SCHEMA" || ! -r "$SCHEMA" ]]; then
    echo 'null'; return
  fi
  raw="$(jq -c '.tools[]? | select(.name=="ExitPlanMode") | .input_schema // null' \
         "$SCHEMA" 2>/dev/null | head -1)"
  [[ -n "$raw" ]] && echo "$raw" || echo 'null'
}

jq -n \
  --arg monitor              "$(probe_tool Monitor)" \
  --arg skill                "$(probe_tool Skill)" \
  --arg exitplanmode         "$(probe_tool ExitPlanMode)" \
  --arg enterplanmode        "$(probe_tool EnterPlanMode)" \
  --arg pushnotification     "$(probe_tool PushNotification)" \
  --arg croncreate           "$(probe_tool CronCreate)" \
  --arg askuserquestion      "$(probe_tool AskUserQuestion)" \
  --arg bash_bg              "$(probe_tool_property Bash run_in_background)" \
  --arg task                 "$(probe_tool Task)" \
  --arg agent                "$(probe_tool Agent)" \
  --arg taskcreate           "$(probe_tool TaskCreate)" \
  --arg taskupdate           "$(probe_tool TaskUpdate)" \
  --arg mcp_github           "$(probe_mcp github)" \
  --arg mcp_gitlab           "$(probe_mcp gitlab)" \
  --arg mcp_ide_diagnostics  "$(probe_tool mcp__ide__getDiagnostics)" \
  --arg mcp_context7         "$(probe_mcp context7)" \
  --argjson epm_schema       "$(exitplanmode_schema)" \
  '{
    schema_version: 1,
    probed_at: (now | strftime("%Y-%m-%dT%H:%M:%SZ")),
    Monitor: {status: $monitor},
    Skill: {status: $skill},
    ExitPlanMode: {status: $exitplanmode, input_schema: $epm_schema},
    EnterPlanMode: {status: $enterplanmode},
    PushNotification: {status: $pushnotification},
    CronCreate: {status: $croncreate},
    AskUserQuestion: {status: $askuserquestion},
    BashRunInBackground: {status: $bash_bg},
    Task: {status: $task},
    Agent: {status: $agent},
    TaskCreate: {status: $taskcreate},
    TaskUpdate: {status: $taskupdate},
    McpGithub: {status: $mcp_github},
    McpGitlab: {status: $mcp_gitlab},
    McpIdeDiagnostics: {status: $mcp_ide_diagnostics},
    McpContext7: {status: $mcp_context7}
  }' >"$OUTPUT"

[[ -n "${TMP_SCHEMA:-}" && -r "${TMP_SCHEMA:-/dev/null}" ]] && rm -f "$TMP_SCHEMA"

cat "$OUTPUT"
