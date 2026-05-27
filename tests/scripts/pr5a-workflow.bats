#!/usr/bin/env bats
# pr5a-workflow.bats — Skills Architecture v1.3 (PR5a)

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  ROOT_CLAUDE="$REPO_ROOT/CLAUDE.md"
  PLAN_MD="$REPO_ROOT/.devcontainer/images/.claude/commands/plan.md"
  SEARCH_MD="$REPO_ROOT/.devcontainer/images/.claude/commands/search.md"
  PROMPT_MD="$REPO_ROOT/.devcontainer/images/.claude/commands/prompt.md"
  SEARCH_GEN="$REPO_ROOT/.devcontainer/images/.claude/commands/search/generate.md"
}

@test "TestPlanGoalFlagInvokesRefine" {
  grep -q '\-\-goal' "$PLAN_MD"
  grep -q 'Skill(skill="refine"' "$PLAN_MD"
}

@test "TestSearchFooterPointsToPlanRefine" {
  grep -q '/search <query> → /plan → /refine → /goal' "$SEARCH_MD"
}

@test "TestPromptHasDeprecationBanner" {
  # PR6 deleted prompt.md outright. The deprecation banner was a PR5a
  # transition step that PR6 made obsolete; the migration doc
  # (.devcontainer/images/.claude/docs/migrations/prompt-to-refine.md)
  # is now the canonical historical record.
  ! [ -e "$PROMPT_MD" ]
}

@test "TestSkillChainPlanToRefine" {
  # The 5th chain wired in PR5a (deferred from PR1)
  grep -q 'Skill(skill="refine"' "$PLAN_MD"
}

@test "TestRootClaudeMdMentionsRefine" {
  grep -q '/refine' "$ROOT_CLAUDE"
  grep -q '/prompt.*\*\*\[DEPRECATED\]\*\*' "$ROOT_CLAUDE" || grep -q '/prompt.*DEPRECATED' "$ROOT_CLAUDE"
}
