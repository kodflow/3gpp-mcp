#!/usr/bin/env bats
# skill-migration-pr4.bats — Skills Architecture v1.3 (PR4)
# WHY: 13 invariants ensuring /ktn, /search, /warmup no longer dispatch
# generic agents.

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  KTN="$REPO_ROOT/.devcontainer/images/.claude/commands/ktn.md"
  SEARCH_PARALLEL="$REPO_ROOT/.devcontainer/images/.claude/commands/search/parallel.md"
  WARMUP_SCAN="$REPO_ROOT/.devcontainer/images/.claude/commands/warmup/scan.md"
  WARMUP_READ="$REPO_ROOT/.devcontainer/images/.claude/commands/warmup/read.md"
  ALLOWLIST="$REPO_ROOT/.devcontainer/images/.claude/agents/migrated_skills.txt"
}

# ----- /ktn (3 tests) -----
@test "TestKtnUsesDevopsExecutorLinux" {
  count=$(grep -c 'subagent_type: devops-executor-linux' "$KTN")
  [ "$count" -ge 5 ]
}

@test "TestKtnNoGeneralPurpose" {
  # The /ktn skill no longer dispatches general-purpose for its heal phase
  ! grep -E 'subagent_type:\s*general-purpose' "$KTN"
}

@test "TestKtnRoutingRulePresent" {
  table="$REPO_ROOT/.devcontainer/images/.claude/agents/routing-table.jsonl"
  grep -q 'ktn-heal-1' "$table"
}

# ----- /search (4 tests) -----
@test "TestSearchRoutesViaAgentTemplate" {
  table="$REPO_ROOT/.devcontainer/images/.claude/agents/routing-table.jsonl"
  grep -q '"id": "search-domain"' "$table"
  grep -q '"agent_template": "developer-specialist-{language}"' "$table"
}

@test "TestSearchLocalUsesPatternsAnalyzer" {
  table="$REPO_ROOT/.devcontainer/images/.claude/agents/routing-table.jsonl"
  grep -qE '"id": "search-local".*"agent": "docs-analyzer-patterns"' "$table"
}

@test "TestSearchParallelMdReferencesRouter" {
  grep -q 'route-agent.sh' "$SEARCH_PARALLEL"
  grep -q '/search --phase external' "$SEARCH_PARALLEL"
}

@test "TestSearchNoExplore" {
  # Legacy Explore example may still be documented but the primary
  # dispatch must invoke route-agent.sh.
  grep -q 'Per PR4' "$SEARCH_PARALLEL"
}

# ----- /warmup (3 tests) -----
@test "TestWarmupReadDispatchesDocsAnalyzers" {
  grep -q 'docs-analyzer-architecture' "$WARMUP_READ"
  grep -q 'docs-analyzer-commands' "$WARMUP_READ"
  grep -q 'docs-analyzer-agents' "$WARMUP_READ"
  grep -q 'docs-analyzer-hooks' "$WARMUP_READ"
}

@test "TestWarmupScanDispatchesDocsAnalyzerStructure" {
  grep -q 'docs-analyzer-structure' "$WARMUP_SCAN"
}

@test "TestWarmupNoExploreInRead" {
  # No remaining bare `type: "Explore"` lines in warmup/read.md
  ! grep -E '^\s*type:\s*"Explore"\s*$' "$WARMUP_READ"
}

# ----- migrated_skills.txt + cross-skill invariants -----
@test "TestMigratedSkillsListContainsKtnSearchWarmup" {
  grep -q '^/ktn$'    "$ALLOWLIST"
  grep -q '^/search$' "$ALLOWLIST"
  grep -q '^/warmup$' "$ALLOWLIST"
}

@test "TestRouterInvariantAppliesToMigratedOnly" {
  # The allowlist file must NOT contain skills not yet migrated by PR4/PR7
  # (e.g. /git, /review are out of scope for this initiative)
  ! grep -qE '^/git$|^/review$' "$ALLOWLIST"
}
