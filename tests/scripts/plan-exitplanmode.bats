#!/usr/bin/env bats
# plan-exitplanmode.bats — Skills Architecture v1.3 (PR1)

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  SYNTH="$REPO_ROOT/.devcontainer/images/.claude/commands/plan/synthesize.md"
}

@test "TestPlanInvokesExitPlanMode" {
  grep -q 'ExitPlanMode(plan=<full md>)' "$SYNTH"
  # Schema validation against PR0 primitives.json is mandatory before the call.
  grep -q '.claude/state/primitives.json' "$SYNTH"
  grep -q 'ExitPlanMode.status == "present"' "$SYNTH"
}
