#!/usr/bin/env bats
# refine-structure.bats — Skills Architecture v1.3 (PR3)
# WHY: assert the /refine skill files and contracts exist as declared in
# the plan. The 28 acceptance tests from context §B.10 require the
# skill to actually run (out of scope for a static contract check);
# this file pins the file layout + key invariants.

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  REFINE_DIR="$REPO_ROOT/.devcontainer/images/.claude/commands/refine"
  REFINE_MD="$REPO_ROOT/.devcontainer/images/.claude/commands/refine.md"
  TABLE="$REPO_ROOT/.devcontainer/images/.claude/agents/routing-table.jsonl"
}

@test "TestRefineMdExists"           { [ -r "$REFINE_MD" ]; }
@test "TestRefineAutoMdExists"       { [ -r "$REFINE_DIR/auto.md" ]; }
@test "TestRefineDispatchMdExists"   { [ -r "$REFINE_DIR/dispatch.md" ]; }
@test "TestRefineSynthesisMdExists"  { [ -r "$REFINE_DIR/synthesis.md" ]; }
@test "TestRefineRenderMdExists"     { [ -r "$REFINE_DIR/render.md" ]; }

@test "TestRefineAllowedTools" {
  # v1.6 amendment: Skill(skill=do) is no longer in allowed-tools; the
  # auto-chain into /do was removed. /refine ends with a printed
  # `Suggested next step: /goal <slug>` instead.
  ! grep -qE '^\s*-\s+"Skill\(skill=do\)"' "$REFINE_MD"
  grep -qE '^\s*-\s+"Write\(\.claude/goals/\*\.md\)"' "$REFINE_MD"
}

@test "TestRefine10LensesRoutingRules" {
  count=$(grep -c '"id": "refine-lens-' "$TABLE")
  [ "$count" -eq 10 ]
}

@test "TestRefineCriticalLensesPresent" {
  grep -q '"matched_rule_id":"static:lens-1-correctness"' \
    <(bash "$REPO_ROOT/.devcontainer/images/.claude/scripts/refine-static-fallback.sh" \
        lens-1-correctness)
}

@test "TestRefineRoutingRulesUseTrue" {
  # WHY (fix #7): /refine lens rules MUST use guard:"true" (or `has(...)`)
  # — never `.project_type != "none"` which matches on missing key.
  ! grep -E '"guard": "\.project_type != "none""' "$TABLE"
  # And every refine-lens rule has guard:"true"
  jq -r 'select(.id | startswith("refine-lens-")) | .guard' "$TABLE" \
    | while read -r g; do
        [ "$g" = "true" ] || { echo "non-true guard: $g"; return 1; }
      done
}

@test "TestRefineDirectiveBudgetDeclared" {
  # v1.6: uniform 4000-char ceiling (no more dual 4096/2000 split).
  # The ceiling is enforced; the natural target is the minimum viable.
  grep -q '4000' "$REFINE_DIR/render.md"
  grep -qE '(ceiling|≤ ?4000)' "$REFINE_DIR/synthesis.md"
}

@test "TestRefineEmitsGoalStateUpdate" {
  grep -q 'goal-state.sh update' "$REFINE_DIR/render.md"
}

@test "TestRefineNoPromptReferences" {
  # Fix #11: /refine output never mentions /prompt
  ! grep -E '/prompt\b' "$REFINE_MD" "$REFINE_DIR"/*.md
}

# 14 structural tests + 12 boundary tests in refine-auto-boundary.bats
# + 1 static-fallback + 2 MD-frontmatter assertions in same file = 37 total
# (28 acceptance from §B.10 are runtime-only and exercised by the skill).
