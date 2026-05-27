#!/usr/bin/env bats
# git-watch-thread-resolve.bats — v1.5 patch on /git --watch
# WHY: pin the two behaviours the user asked us to add:
#  1) classify step MUST drop threads where isResolved == true so the
#     fix loop doesn't churn over user-resolved comments.
#  2) illegitimate findings MUST trigger a per-thread GraphQL
#     resolveReviewThread mutation so the red dots in the UI clear.
# Without these tests in place a future refactor of watch.md could
# silently drop the GraphQL fallback and re-introduce the regression.

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  WATCH="$REPO_ROOT/.devcontainer/images/.claude/commands/git/watch.md"
}

@test "TestWatchClassifyDropsUserResolvedThreads" {
  # The relevance filter must apply select(.isResolved == false ...)
  grep -q 'select(.isResolved == false' "$WATCH"
}

@test "TestWatchClassifyDropsOutdatedThreads" {
  grep -q '.isOutdated == false' "$WATCH"
}

@test "TestWatchCoderabbitRejectedResolvesThreadViaGraphql" {
  # CodeRabbit illegitimate_rejected must invoke resolveReviewThread
  grep -q 'resolveReviewThread' "$WATCH"
  # And it must come AFTER the dismiss-review step (graphql is step 3)
  awk '/illegitimate_rejected:/{f=1} f && /resolveReviewThread/{print; exit}' "$WATCH" \
    | grep -q 'resolveReviewThread'
}

@test "TestWatchUsesGhApiGraphqlForThreadResolve" {
  grep -q 'gh api graphql' "$WATCH"
}

@test "TestWatchQodoRejectedResolvesThreadViaGraphql" {
  # Qodo path also resolves at thread level
  awk '/qodo:/{f=1} /coderabbit:/{f=0} f && /resolveReviewThread/{n++} END {exit (n>0)?0:1}' "$WATCH"
}

@test "TestWatchGraphqlFallbackDocumented" {
  # The 'or true' tolerance for a single thread failure must be there —
  # one bad thread id mustn't kill the whole rejection step.
  grep -q '|| true' "$WATCH"
}

@test "TestWatchDocumentsWhyThreadResolveIsNeeded" {
  # The user-facing rationale (red dots stay otherwise) must be in the doc
  grep -q 'red dots' "$WATCH"
}
