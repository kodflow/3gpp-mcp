#!/usr/bin/env bats
# pr7-longtail.bats — Skills Architecture v1.3 (PR7)

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  PLAN_EXPLORE="$REPO_ROOT/.devcontainer/images/.claude/commands/plan/explore.md"
  FEATURE_AUDIT="$REPO_ROOT/.devcontainer/images/.claude/commands/feature/audit.md"
  TEST_MD="$REPO_ROOT/.devcontainer/images/.claude/commands/test.md"
  INFRA_MD="$REPO_ROOT/.devcontainer/images/.claude/commands/infra.md"
  COMMENT_MD="$REPO_ROOT/.devcontainer/images/.claude/commands/comment.md"
  ALLOWLIST="$REPO_ROOT/.devcontainer/images/.claude/agents/migrated_skills.txt"
}

@test "TestPlanExplorersRouted" {
  grep -q 'docs-analyzer-architecture' "$PLAN_EXPLORE"
  grep -q 'docs-analyzer-patterns' "$PLAN_EXPLORE"
  # Old generic Explore inline still mentioned for reference but the
  # primary dispatch types are docs-analyzer-*
  count=$(grep -c '^\s*type:\s*"docs-analyzer-' "$PLAN_EXPLORE")
  [ "$count" -ge 4 ]
}

@test "TestFeatureAuditRouted" {
  grep -q 'docs-analyzer-architecture' "$FEATURE_AUDIT"
  grep -q 'PR7' "$FEATURE_AUDIT"
}

@test "TestTestFrameworkRouted" {
  grep -q 'developer-specialist-playwright' "$TEST_MD"
  grep -q 'route-agent.sh' "$TEST_MD"
}

@test "TestInfraCloudRouted" {
  grep -q 'devops-specialist-cloudflare' "$INFRA_MD"
  grep -q 'route-agent.sh' "$INFRA_MD"
}

@test "TestCommentLangRouted" {
  grep -q 'route-agent.sh' "$COMMENT_MD"
  grep -q 'PR7' "$COMMENT_MD"
}

@test "TestMigratedSkillsListContainsLongTail" {
  for skill in /plan /feature /test /infra /comment; do
    grep -q "^$skill$" "$ALLOWLIST" || { echo "missing $skill"; return 1; }
  done
}
