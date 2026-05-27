#!/usr/bin/env bats
# pr5b-runtime-primitives.bats — Skills Architecture v1.3 (PR5b)
# WHY: ensure Monitor + PushNotification are referenced in the 4 skills
# the plan calls out. Tests are gated by PR0 primitives.json — they
# `skip` if the corresponding primitive is `absent` rather than failing.

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  WATCH="$REPO_ROOT/.devcontainer/images/.claude/commands/git/watch.md"
  LOOP="$REPO_ROOT/.devcontainer/images/.claude/commands/do/loop.md"
  MERGE="$REPO_ROOT/.devcontainer/images/.claude/commands/git/merge.md"
  TERRAFORM="$REPO_ROOT/.devcontainer/images/.claude/commands/infra/terraform.md"
}

primitive_status() {
  local p="$1"
  local file="$REPO_ROOT/.claude/state/primitives.json"
  [ -r "$file" ] || { echo unknown; return; }
  jq -r --arg p "$p" '.[$p].status // "unknown"' "$file"
}

@test "TestGitWatchUsesMonitor" {
  [ "$(primitive_status Monitor)" = "absent" ] && skip "Monitor absent — fallback used"
  grep -q 'Monitor primitive' "$WATCH"
  grep -q 'Monitor(' "$WATCH"
}

@test "TestDoLoopEmitsPushNotification" {
  [ "$(primitive_status PushNotification)" = "absent" ] && skip "PushNotification absent"
  grep -q 'PushNotification' "$LOOP"
  grep -q 'terminal_notify' "$LOOP"
}

@test "TestInfraApplyUsesMonitor" {
  [ "$(primitive_status Monitor)" = "absent" ] && skip "Monitor absent"
  grep -q 'Monitor(' "$TERRAFORM"
  grep -q 'terraform apply' "$TERRAFORM"
}

@test "TestGitMergeEmitsPushNotificationOnComplete" {
  [ "$(primitive_status PushNotification)" = "absent" ] && skip "PushNotification absent"
  grep -q 'PushNotification primitive' "$MERGE" || grep -q 'PushNotification' "$MERGE"
  grep -q 'Merged' "$MERGE"
}
