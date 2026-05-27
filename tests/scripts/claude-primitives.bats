#!/usr/bin/env bats
# claude-primitives.bats — Skills Architecture v1.3 (PR0)
# WHY: assert probe-primitives.sh emits a status for every primitive the
# initiative depends on, plus the ExitPlanMode schema record consumed by PR1.

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  PROBE="$REPO_ROOT/.devcontainer/images/.claude/scripts/probe-primitives.sh"
  COMPAT="$REPO_ROOT/.devcontainer/images/.claude/docs/primitives-compat.md"
  TMP="$(mktemp -d)"
  STATE_FILE="$TMP/primitives.json"

  # Stub schema describing every primitive the initiative consumes
  SCHEMA="$TMP/tool-schema.json"
  cat >"$SCHEMA" <<'JSON'
{
  "tools": [
    {"name": "Monitor"},
    {"name": "Skill"},
    {"name": "ExitPlanMode", "input_schema": {"properties": {"plan": {"type": "string"}}}},
    {"name": "EnterPlanMode"},
    {"name": "PushNotification"},
    {"name": "CronCreate"},
    {"name": "AskUserQuestion"},
    {"name": "Bash", "input_schema": {"properties": {"run_in_background": {"type": "boolean"}}}},
    {"name": "Task"},
    {"name": "Agent"},
    {"name": "TaskCreate"},
    {"name": "TaskUpdate"},
    {"name": "mcp__ide__getDiagnostics"}
  ]
}
JSON

  MCP="$TMP/mcp.json"
  cat >"$MCP" <<'JSON'
{"mcpServers": {"github": {}, "gitlab": {}, "context7": {}}}
JSON

  bash "$PROBE" --schema "$SCHEMA" --mcp-config "$MCP" --output "$STATE_FILE" >/dev/null
}

teardown() { rm -rf "$TMP"; }

primitive_status() { jq -r --arg p "$1" '.[$p].status' "$STATE_FILE"; }

@test "TestClaudePrimitiveAvailability_Monitor"              { [ "$(primitive_status Monitor)" = "present" ]; }
@test "TestClaudePrimitiveAvailability_Skill"                { [ "$(primitive_status Skill)" = "present" ]; }
@test "TestClaudePrimitiveAvailability_ExitPlanMode"         { [ "$(primitive_status ExitPlanMode)" = "present" ]; }
@test "TestClaudePrimitiveAvailability_EnterPlanMode"        { [ "$(primitive_status EnterPlanMode)" = "present" ]; }
@test "TestClaudePrimitiveAvailability_PushNotification"     { [ "$(primitive_status PushNotification)" = "present" ]; }
@test "TestClaudePrimitiveAvailability_CronCreate"           { [ "$(primitive_status CronCreate)" = "present" ]; }
@test "TestClaudePrimitiveAvailability_AskUserQuestion"      { [ "$(primitive_status AskUserQuestion)" = "present" ]; }
@test "TestClaudePrimitiveAvailability_BashRunInBackground"  { [ "$(primitive_status BashRunInBackground)" = "present" ]; }
@test "TestClaudePrimitiveAvailability_Task"                 { [ "$(primitive_status Task)" = "present" ]; }
@test "TestClaudePrimitiveAvailability_Agent"                { [ "$(primitive_status Agent)" = "present" ]; }
@test "TestClaudePrimitiveAvailability_TaskCreate"           { [ "$(primitive_status TaskCreate)" = "present" ]; }
@test "TestClaudePrimitiveAvailability_TaskUpdate"           { [ "$(primitive_status TaskUpdate)" = "present" ]; }
@test "TestClaudePrimitiveAvailability_McpGithub"            { [ "$(primitive_status McpGithub)" = "present" ]; }
@test "TestClaudePrimitiveAvailability_McpGitlab"            { [ "$(primitive_status McpGitlab)" = "present" ]; }
@test "TestClaudePrimitiveAvailability_McpIdeDiagnostics"    { [ "$(primitive_status McpIdeDiagnostics)" = "present" ]; }
@test "TestClaudePrimitiveAvailability_McpContext7"          { [ "$(primitive_status McpContext7)" = "present" ]; }

@test "TestPrimitivesCompatDocExists" {
  [ -r "$COMPAT" ]
  for p in Monitor Skill ExitPlanMode EnterPlanMode PushNotification CronCreate AskUserQuestion \
           "Bash run_in_background" Task Agent TaskCreate TaskUpdate \
           "mcp__github__\*" "mcp__gitlab__\*" "mcp__ide__getDiagnostics" "mcp__context7__\*"; do
    grep -q "^### $p$" "$COMPAT" || { echo "missing section for $p"; return 1; }
  done
}

@test "TestExitPlanModeSchemaRecorded" {
  jq -e '.ExitPlanMode.input_schema.properties.plan.type == "string"' "$STATE_FILE"
}

@test "TestProbeReturnsUnknownWhenSchemaAbsent" {
  out="$TMP/no-schema.json"
  bash "$PROBE" --schema "/nonexistent" --mcp-config "$MCP" --output "$out" >/dev/null
  [ "$(jq -r '.Monitor.status' "$out")" = "unknown" ]
}
