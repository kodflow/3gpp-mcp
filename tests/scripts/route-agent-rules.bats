#!/usr/bin/env bats
# route-agent-rules.bats — Skills Architecture v1.3 (PR2a + PR2b)
# WHY: lock down router exit codes, priority order, fanout semantics
# and the agent_template expansion contract so a future rewrite cannot
# silently break the routing layer.

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  ROUTER="$REPO_ROOT/.devcontainer/images/.claude/scripts/route-agent.sh"
  TABLE_DEFAULT="$REPO_ROOT/.devcontainer/images/.claude/agents/routing-table.jsonl"
  TMP="$(mktemp -d)"
  export CLAUDE_AGENTS_DIR="$REPO_ROOT/.devcontainer/images/.claude/agents"
  export CLAUDE_ROUTING_TABLE="$TABLE_DEFAULT"
  export CLAUDE_ROUTING_TELEMETRY="$TMP/router-fallbacks.jsonl"
  GO_PROFILE="$REPO_ROOT/tests/fixtures/profiles/go.json"
  MULTI_PROFILE="$REPO_ROOT/tests/fixtures/profiles/multi-lang.json"
  IAC_PROFILE="$REPO_ROOT/tests/fixtures/profiles/iac-aws.json"
  K8S_PROFILE="$REPO_ROOT/tests/fixtures/profiles/k8s.json"
}
teardown() { rm -rf "$TMP"; }

@test "TestRouterPriorityOrderHigherWins" {
  # IAC fixture has rule priority 100 ("test-fixture-iac") + would also fall
  # back to the github CI rule (70); top priority must win.
  run bash "$ROUTER" --skill /test-skill --phase fixture-iac --profile "$IAC_PROFILE"
  [ "$status" -eq 0 ]
  echo "$output" | jq -e '.[0].matched_rule_id == "test-fixture-iac"'
}

@test "TestRouterTiebreakByRuleIdLexicographic" {
  # Create a fake table with two same-priority rules and assert id ASC wins.
  cat > "$TMP/tbl.jsonl" <<'JSONL'
{"id": "z-second", "priority": 50, "skill": "/test", "phase": "fixture", "guard": "true", "agent": "developer-specialist-go", "effort": "medium", "fanout": false}
{"id": "a-first",  "priority": 50, "skill": "/test", "phase": "fixture", "guard": "true", "agent": "developer-specialist-go", "effort": "medium", "fanout": false}
JSONL
  CLAUDE_ROUTING_TABLE="$TMP/tbl.jsonl" run bash "$ROUTER" --skill /test --phase fixture --profile "$GO_PROFILE"
  [ "$status" -eq 0 ]
  echo "$output" | jq -e '.[0].matched_rule_id == "a-first"'
}

@test "TestRouterFanoutEmitsAllAtTopPriority" {
  run bash "$ROUTER" --skill /test-skill --phase fixture-multi --profile "$MULTI_PROFILE"
  [ "$status" -eq 0 ]
  count=$(echo "$output" | jq 'length')
  [ "$count" -ge 2 ]
}

@test "TestRouterFanoutFalseEmitsTopOnly" {
  run bash "$ROUTER" --skill /test-skill --phase fixture --profile "$GO_PROFILE"
  [ "$status" -eq 0 ]
  count=$(echo "$output" | jq 'length')
  [ "$count" -eq 1 ]
}

@test "TestRouterDeterministic" {
  out1=$(bash "$ROUTER" --skill /test-skill --phase fixture-multi --profile "$MULTI_PROFILE")
  out2=$(bash "$ROUTER" --skill /test-skill --phase fixture-multi --profile "$MULTI_PROFILE")
  [ "$out1" = "$out2" ]
}

@test "TestRouterDryRunExplainsMatchedRules" {
  run bash "$ROUTER" --skill /test-skill --phase fixture --profile "$GO_PROFILE" --dry-run
  [ "$status" -eq 0 ]
  echo "$output" | jq -e '.matched_rules and .dispatch'
}

@test "TestRouterFallbackTelemetryRedactsPaths" {
  # phase=none triggers no-match fallback
  bash "$ROUTER" --skill /test-skill --phase nonexistent --profile "$GO_PROFILE" >/dev/null || true
  [ -r "$CLAUDE_ROUTING_TELEMETRY" ]
  ! grep -q "$HOME" "$CLAUDE_ROUTING_TELEMETRY"
}

@test "TestRoutingTableNoModelField" {
  # WHY: model is resolved from agent frontmatter; rules MUST NOT hard-code it.
  ! jq -e 'select(.model)' "$TABLE_DEFAULT" >/dev/null
}

@test "TestRouterExpandsAgentTemplateFromLanguage" {
  run bash "$ROUTER" --skill /test-skill --phase fixture-multi --profile "$MULTI_PROFILE"
  [ "$status" -eq 0 ]
  echo "$output" | jq -e '.[].expanded_from_template == true' >/dev/null
  echo "$output" | jq -e '.[].subagent_type | startswith("developer-specialist-")' >/dev/null
}

@test "TestRouterRejectsTemplateWithoutExpandFrom" {
  cat > "$TMP/bad.jsonl" <<'JSONL'
{"id": "broken", "priority": 50, "skill": "/test", "phase": "fixture", "guard": "true", "agent_template": "developer-specialist-{language}", "effort": "medium", "fanout": true}
JSONL
  CLAUDE_ROUTING_TABLE="$TMP/bad.jsonl" run bash "$ROUTER" --skill /test --phase fixture --profile "$GO_PROFILE"
  [ "$status" -eq 23 ]
}

@test "TestRouterRejectsExpandedAgentWithoutFrontmatter" {
  cat > "$TMP/bad.jsonl" <<'JSONL'
{"id": "expand-missing", "priority": 50, "skill": "/test", "phase": "fixture-multi", "guard": "true", "agent_template": "developer-specialist-{language}", "expand_from": ".languages[].name", "effort": "medium", "fanout": true}
JSONL
  # Stage one fake profile language that has no specialist file at all.
  cat > "$TMP/fakelang.json" <<'JSON'
{"languages":[{"name":"klingon","marker":"k.lon","primary":true}],"tools":{},"cloud":[],"container":[],"k8s":false,"os":"linux","ci":"none","test_frameworks":[],"project_type":"code"}
JSON
  CLAUDE_ROUTING_TABLE="$TMP/bad.jsonl" run bash "$ROUTER" --skill /test --phase fixture-multi --profile "$TMP/fakelang.json"
  [ "$status" -eq 30 ]
}
