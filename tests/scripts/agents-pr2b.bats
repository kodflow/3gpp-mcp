#!/usr/bin/env bats
# agents-pr2b.bats — Skills Architecture v1.3 (PR2b)
# WHY: assert the 5 new specialist agents exist with valid frontmatter
# and the router actually dispatches them on the matching profile.

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  AGENTS="$REPO_ROOT/.devcontainer/images/.claude/agents"
  ROUTER="$REPO_ROOT/.devcontainer/images/.claude/scripts/route-agent.sh"
  TMP="$(mktemp -d)"
  export CLAUDE_AGENTS_DIR="$AGENTS"
  export CLAUDE_ROUTING_TABLE="$AGENTS/routing-table.jsonl"
  export CLAUDE_ROUTING_TELEMETRY="$TMP/telemetry.jsonl"
}
teardown() { rm -rf "$TMP"; }

assert_agent_valid() {
  local agent="$1"
  local file="$AGENTS/$agent.md"
  [ -r "$file" ]
  awk '/^---$/{c++; next} c==1{print}' "$file" | grep -qE '^model: (haiku|sonnet|opus)'
  awk '/^---$/{c++; next} c==1{print}' "$file" | grep -qE '^name: '"$agent"
}

@test "TestAgentReactExists"          { assert_agent_valid developer-specialist-react; }
@test "TestAgentPostgresExists"       { assert_agent_valid data-specialist-postgres; }
@test "TestAgentPlaywrightExists"     { assert_agent_valid developer-specialist-playwright; }
@test "TestAgentCloudflareExists"     { assert_agent_valid devops-specialist-cloudflare; }
@test "TestAgentGithubActionsExists"  { assert_agent_valid tooling-specialist-github-actions; }

@test "TestRouterDispatchesReactForNodejsWithReactDep" {
  cat > "$TMP/p.json" <<'JSON'
{"languages":[{"name":"nodejs","marker":"package.json","primary":true}],
 "tools":{"react":true,"playwright":false},
 "cloud":[],"container":[],"k8s":false,"os":"linux","ci":"none",
 "test_frameworks":[],"project_type":"code"}
JSON
  run bash "$ROUTER" --skill /lint --phase explore --profile "$TMP/p.json"
  [ "$status" -eq 0 ]
  echo "$output" | jq -e '.[] | select(.subagent_type == "developer-specialist-react")' >/dev/null
}

@test "TestRouterDispatchesPostgresForPsqlPresent" {
  cat > "$TMP/p.json" <<'JSON'
{"languages":[{"name":"go","marker":"go.mod","primary":true}],
 "tools":{"psql":true,"react":false,"playwright":false},
 "cloud":[],"container":[],"k8s":false,"os":"linux","ci":"none",
 "test_frameworks":[],"project_type":"code"}
JSON
  run bash "$ROUTER" --skill /lint --phase any --profile "$TMP/p.json"
  [ "$status" -eq 0 ]
  echo "$output" | jq -e '.[] | select(.subagent_type == "data-specialist-postgres")' >/dev/null
}

@test "TestRouterDispatchesPlaywrightForTestSkillE2E" {
  cat > "$TMP/p.json" <<'JSON'
{"languages":[{"name":"nodejs","marker":"package.json","primary":true}],
 "tools":{"react":false,"playwright":true},
 "cloud":[],"container":[],"k8s":false,"os":"linux","ci":"none",
 "test_frameworks":["playwright"],"project_type":"code"}
JSON
  run bash "$ROUTER" --skill /test --phase e2e --profile "$TMP/p.json"
  [ "$status" -eq 0 ]
  echo "$output" | jq -e '.[] | select(.subagent_type == "developer-specialist-playwright")' >/dev/null
}

@test "TestRouterDispatchesCloudflareForInfraSkillWithWrangler" {
  cat > "$TMP/p.json" <<'JSON'
{"languages":[{"name":"nodejs","marker":"package.json","primary":true}],
 "tools":{"wrangler":true,"react":false,"playwright":false},
 "cloud":["cloudflare"],"container":[],"k8s":false,"os":"linux","ci":"none",
 "test_frameworks":[],"project_type":"code"}
JSON
  run bash "$ROUTER" --skill /infra --phase any --profile "$TMP/p.json"
  [ "$status" -eq 0 ]
  echo "$output" | jq -e '.[] | select(.subagent_type == "devops-specialist-cloudflare")' >/dev/null
}

@test "TestRouterDispatchesGithubActionsForCISkillGithub" {
  cat > "$TMP/p.json" <<'JSON'
{"languages":[{"name":"go","marker":"go.mod","primary":true}],
 "tools":{"react":false,"playwright":false},
 "cloud":[],"container":[],"k8s":false,"os":"linux","ci":"github",
 "test_frameworks":[],"project_type":"code"}
JSON
  run bash "$ROUTER" --skill /git --phase ci --profile "$TMP/p.json"
  [ "$status" -eq 0 ]
  echo "$output" | jq -e '.[] | select(.subagent_type == "tooling-specialist-github-actions")' >/dev/null
}
