#!/usr/bin/env bats
# skill-chain.bats — Skills Architecture v1.3 (PR1)
# WHY: assert each of the 4 active chains uses the real Skill tool rather
# than magic-string text. Two recursion-safety tests cap chain depth.
# The 5th chain (/plan → /refine) is deferred to PR5a.

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  CYCLIC="$REPO_ROOT/.devcontainer/images/.claude/commands/review/cyclic.md"
  COMMIT="$REPO_ROOT/.devcontainer/images/.claude/commands/git/commit.md"
  INIT="$REPO_ROOT/.devcontainer/images/.claude/commands/init/discovery.md"
  SEARCH="$REPO_ROOT/.devcontainer/images/.claude/commands/search/generate.md"
}

@test "TestSkillChainReviewToDo" {
  grep -q 'Skill(skill="do"' "$CYCLIC"
}

@test "TestSkillChainCommitToReview" {
  grep -q 'Skill(skill="review", args="--staged")' "$COMMIT"
}

@test "TestSkillChainInitToWarmup" {
  grep -q 'Skill(skill="warmup")' "$INIT"
}

@test "TestSkillChainSearchToPlan" {
  grep -q 'Skill(skill="plan", args="--context {slug}")' "$SEARCH"
}

@test "TestSkillRecursionMaxDepth" {
  # WHY: the guard is documented in cyclic.md so future refactors don't
  # silently remove the depth cap. Actual enforcement lives in team-mode
  # primitives at runtime (out of scope for a static doc test).
  grep -q 'max depth 5' "$CYCLIC"
}

@test "TestSkillRecursionDetectsCycleGitReviewGit" {
  # The cycle-detection doctrine is repeated where it matters: in commit
  # which would otherwise loop /git → /review → /git via the post-commit
  # chain.
  grep -q 'cycle-detection' "$COMMIT"
}
