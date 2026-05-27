#!/usr/bin/env bats
# do-goal-turn.bats — Skills Architecture v1.3 (PR1)
# WHY: pin the documented contract for --goal-turn so a future refactor
# of /do doesn't silently drop the goal-state lifecycle.

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  DO_MD="$REPO_ROOT/.devcontainer/images/.claude/commands/do.md"
  PLAN_DETECT="$REPO_ROOT/.devcontainer/images/.claude/commands/do/plan-detect.md"
  QUESTIONS="$REPO_ROOT/.devcontainer/images/.claude/commands/do/questions.md"
}

@test "TestDoGoalTurnFlagAccepted" {
  grep -q '`--goal-turn <slug>`' "$DO_MD"
}

@test "TestDoGoalTurnReadsStateFile" {
  grep -q 'goal-state.sh read' "$PLAN_DETECT"
}

@test "TestDoGoalTurnRefusesNoState" {
  grep -q 'exit code 4' "$PLAN_DETECT"
}

@test "TestDoGoalTurnSkipsQuestionsPhase" {
  grep -q "mode == \"GOAL_TURN\"" "$QUESTIONS"
}

@test "TestDoGoalTurnSkipsWorktreeConfirm" {
  grep -q 'skip_phases: \[3.0, 5.5\]' "$PLAN_DETECT"
}

@test "TestDoGoalTurnPersistsState" {
  grep -q 'goal-state.sh update <slug>' "$PLAN_DETECT"
}

@test "TestDoGoalTurnEmitsGoalCondition" {
  grep -q 'goal-condition:' "$PLAN_DETECT"
}

@test "TestDoGoalTurnHonorsMaxIterations" {
  grep -q 'iteration >= max_iterations' "$PLAN_DETECT"
}

@test "TestDoGoalTurnMarksStaleAfter24h" {
  grep -q 'GOAL_STALE_AFTER_HOURS' "$PLAN_DETECT"
}

@test "TestDoGoalTurnRefusesCompletedState" {
  grep -q 'status in {completed, abandoned}' "$PLAN_DETECT"
}
