#!/usr/bin/env bats
# pr8-scheduled.bats — Skills Architecture v1.3 (PR8)

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  AUDIT="$REPO_ROOT/.devcontainer/images/.claude/commands/audit.md"
  LEARN="$REPO_ROOT/.devcontainer/images/.claude/commands/learn.md"
  FEATURE="$REPO_ROOT/.devcontainer/images/.claude/commands/feature.md"
  LINT="$REPO_ROOT/.devcontainer/images/.claude/commands/lint.md"
  KTN="$REPO_ROOT/.devcontainer/images/.claude/commands/ktn.md"
  DO_LOOP="$REPO_ROOT/.devcontainer/images/.claude/commands/do/loop.md"
}

@test "TestAuditWatchModeCronCreates" {
  grep -q 'CronCreate' "$AUDIT"
  grep -q '13 9 \* \* 1' "$AUDIT"
}

@test "TestLearnSessionEndAutoPropose" {
  grep -q 'SessionEnd' "$LEARN"
  grep -q '>=3 user corrections' "$LEARN"
}

@test "TestFeatureGhSyncMirrors" {
  grep -q '\-\-gh-sync' "$FEATURE"
  grep -q 'mcp__github__issue_write' "$FEATURE"
  grep -q 'mcp__github__sub_issue_write' "$FEATURE"
}

@test "TestLintWatchUsesInotifyMonitor" {
  grep -q 'inotifywait' "$LINT"
  grep -q 'Monitor(' "$LINT"
}

@test "TestKtnDailyCronHealthProbe" {
  grep -q 'schedule-daily\|CronCreate' "$KTN"
  grep -q '7 8 \* \* \*' "$KTN"
}

@test "TestLongOpsEmitPushNotificationOnExit" {
  grep -q 'PushNotification' "$DO_LOOP"
}
