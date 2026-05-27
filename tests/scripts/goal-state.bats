#!/usr/bin/env bats
# goal-state.bats — Skills Architecture v1.3 (PR1)

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  SCRIPT="$REPO_ROOT/.devcontainer/images/.claude/scripts/goal-state.sh"
  TMP="$(mktemp -d)"
  export GOAL_STATE_DIR="$TMP"
  export CLAUDE_SESSION_ID="session-test"
}
teardown() { rm -rf "$TMP"; }

@test "TestGoalStateCreate" {
  run bash "$SCRIPT" create slug-a .claude/plans/x.md .claude/contexts/x.md
  [ "$status" -eq 0 ]
  [ -r "$TMP/slug-a.json" ]
  [ "$(jq -r '.slug' "$TMP/slug-a.json")" = "slug-a" ]
  [ "$(jq -r '.iteration' "$TMP/slug-a.json")" = "0" ]
  [ "$(jq -r '.status' "$TMP/slug-a.json")" = "active" ]
}

@test "TestGoalStateUpdate" {
  bash "$SCRIPT" create slug-b >/dev/null
  bash "$SCRIPT" update slug-b --iteration 2 --decision met --decision-reason "tests-green" >/dev/null
  [ "$(jq -r '.iteration' "$TMP/slug-b.json")" = "2" ]
  [ "$(jq -r '.last_decision' "$TMP/slug-b.json")" = "met" ]
}

@test "TestGoalStateMarkStale" {
  bash "$SCRIPT" create slug-c >/dev/null
  # Force last_updated_at way in the past
  tmp_obj=$(jq '.last_updated_at = "2000-01-01T00:00:00Z"' "$TMP/slug-c.json")
  echo "$tmp_obj" > "$TMP/slug-c.json"
  run bash "$SCRIPT" mark-stale slug-c
  [ "$output" = "stale" ]
  [ "$(jq -r '.status' "$TMP/slug-c.json")" = "stale" ]
}

@test "TestGoalStateGarbageCollect" {
  bash "$SCRIPT" create slug-d >/dev/null
  jq '.status = "completed" | .last_updated_at = "2000-01-01T00:00:00Z"' "$TMP/slug-d.json" \
    > "$TMP/slug-d.json.tmp" && mv "$TMP/slug-d.json.tmp" "$TMP/slug-d.json"
  run bash "$SCRIPT" gc
  [ "$status" -eq 0 ]
  [ ! -e "$TMP/slug-d.json" ]
}

@test "TestGoalStateCollisionSameSha" {
  bash "$SCRIPT" create slug-e >/dev/null
  run bash "$SCRIPT" create slug-e
  [ "$status" -eq 3 ]
}
