#!/usr/bin/env bats
# git-merge-mcp.bats — Skills Architecture v1.3 (PR1)

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  MERGE_MD="$REPO_ROOT/.devcontainer/images/.claude/commands/git/merge.md"
  GUARDRAILS="$REPO_ROOT/.devcontainer/images/.claude/commands/git/guardrails.md"
}

@test "TestGitMergeUsesMcpNotCli" {
  # gh pr merge must be on the forbidden list AND not present in the merge call section
  grep -q '"gh pr merge"' "$MERGE_MD"
  grep -q 'mcp__github__merge_pull_request' "$MERGE_MD"
  ! grep -E '^\s*gh pr merge ' "$MERGE_MD"
}

@test "TestGuardrailsDocumentClaudePatterns" {
  grep -q '\.claude/.* path reference' "$GUARDRAILS"
  grep -q '\^\\s\*plan:' "$GUARDRAILS"
  grep -q '#369' "$GUARDRAILS"
}
