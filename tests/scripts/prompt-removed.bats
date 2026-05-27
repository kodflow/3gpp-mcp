#!/usr/bin/env bats
# prompt-removed.bats — Skills Architecture v1.3 (PR6, fix #10)
# WHY: lock in the deletion of /prompt and enforce the allowlist using
# relative paths (the v1.2 regex failed against `grep -rln` absolute output).

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
}

@test "TestPromptCommandRemoved" {
  ! [ -e "$REPO_ROOT/.devcontainer/images/.claude/commands/prompt.md" ]
}

@test "TestPromptNoReferences" {
  # WHY: /prompt was deleted in PR6. Pre-existing docs that REFERENCE the
  # historical skill (changelogs, migration records, command inventories
  # documenting the change) are legitimate — they record the deletion
  # rather than depend on the skill. The allowlist captures every file
  # where the mention is documentation-of-the-removal, not actual usage.
  local violations
  violations=$(
    cd "$REPO_ROOT" &&
    grep -rln '/prompt' \
      --include="*.md" \
      --exclude-dir=.git \
      --exclude-dir=node_modules \
      . \
    | sed 's#^\./##' \
    | grep -vE '^(CHANGELOG\.md|CLAUDE\.md|docs/commands/README\.md|\.devcontainer/images/CLAUDE\.md|\.devcontainer/images/\.claude/CLAUDE\.md|\.devcontainer/images/\.claude/docs/migrations/prompt-to-refine\.md|\.devcontainer/features/claude/CLAUDE\.md|\.claude/plans/.*|tests/scripts/prompt-removed\.bats|tests/scripts/pr5a-workflow\.bats|tests/scripts/refine-v14-modes\.bats|\.claude/.+)$' \
    || true
  )
  [ -z "$violations" ] || { echo "Stale /prompt references:"; echo "$violations"; return 1; }
}

@test "TestPromptAllowlistUsesRelativePaths" {
  # WHY (fix #10): the test file itself must strip the leading ./ before
  # matching, otherwise the allowlist regex would silently mismatch.
  grep -q "sed 's#\^\\\\.\\/##'" "$BATS_TEST_FILENAME"
}
