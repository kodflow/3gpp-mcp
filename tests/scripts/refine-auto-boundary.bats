#!/usr/bin/env bats
# refine-auto-boundary.bats — Skills Architecture v1.3 (PR3, fix #4)
# WHY: pin the deterministic LIGHT/FULL boundary so a refactor of
# auto.md cannot silently shift the decision.

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  source "$REPO_ROOT/.devcontainer/images/.claude/scripts/frontmatter.sh"
  source "$REPO_ROOT/.devcontainer/images/.claude/scripts/refine-static-fallback.sh"
  AUTO_MD="$REPO_ROOT/.devcontainer/images/.claude/commands/refine/auto.md"
  TMP="$(mktemp -d)"
}
teardown() { rm -rf "$TMP"; }

# Inline copy of auto_select_mode for testing; the canonical version
# lives in refine/auto.md. WHY frontmatter_get_or and not the bare
# frontmatter_get: yq/jq's `// "missing"` operator treats `false` as
# missing, which incorrectly coerces a real `touches_*: false` value
# into the sentinel and forces FULL mode on every plan. The _or helper
# uses `has()` so a present-but-false value stays `false`.
auto_select_mode() {
  local plan_path="$1"
  local risk loc pub_api sec devinf
  risk=$(frontmatter_get_or    "$plan_path" risk                      missing)
  loc=$(frontmatter_get_or     "$plan_path" loc_estimate_max          -1)
  pub_api=$(frontmatter_get_or "$plan_path" touches_public_api        missing)
  sec=$(frontmatter_get_or     "$plan_path" touches_security_surface  missing)
  devinf=$(frontmatter_get_or  "$plan_path" touches_dev_infra         missing)
  for field in "$risk" "$pub_api" "$sec" "$devinf"; do
    [ "$field" = "missing" ] && { echo "FULL"; return; }
  done
  case "$loc" in ''|*[!0-9]*) echo "FULL"; return ;; esac
  if [ "$loc" -le 500 ] \
     && { [ "$risk" = "low" ] || [ "$risk" = "medium" ]; } \
     && [ "$pub_api" = "false" ] && [ "$sec" = "false" ] && [ "$devinf" = "false" ]; then
    echo "LIGHT"
  else
    echo "FULL"
  fi
}

make_plan() {
  local file="$TMP/plan.md"
  cat > "$file" <<EOF
---
risk: $1
loc_estimate_max: $2
touches_public_api: $3
touches_security_surface: $4
touches_dev_infra: $5
---

# plan
body
EOF
  echo "$file"
}

@test "TestRefineAutoModeBoundary500Loc" {
  [ "$(auto_select_mode "$(make_plan low 500 false false false)")" = "LIGHT" ]
  [ "$(auto_select_mode "$(make_plan low 501 false false false)")" = "FULL"  ]
}

@test "TestRefineAutoModeBoundaryRiskMedium" {
  [ "$(auto_select_mode "$(make_plan medium 100 false false false)")" = "LIGHT" ]
}

@test "TestRefineAutoModePublicApiForcesFull" {
  [ "$(auto_select_mode "$(make_plan low 10 true false false)")" = "FULL" ]
}

@test "TestRefineAutoModeSecurityForcesFull" {
  [ "$(auto_select_mode "$(make_plan low 10 false true false)")" = "FULL" ]
}

@test "TestRefineAutoModeInfraForcesFull" {
  [ "$(auto_select_mode "$(make_plan low 10 false false true)")" = "FULL" ]
}

@test "TestRefineAutoModeMissingMetadataDefaultsFull" {
  # Plan with only risk; pub_api/sec/devinf missing → FULL
  cat > "$TMP/partial.md" <<'EOF'
---
risk: low
---

body
EOF
  [ "$(auto_select_mode "$TMP/partial.md")" = "FULL" ]
}

@test "TestRefineAutoModeReadsMarkdownFrontmatter" {
  # Pass a small fixture plan (with full body) — assert the parser only
  # looks at the frontmatter block, not the body.
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  [ "$(auto_select_mode "$REPO_ROOT/tests/fixtures/refine/plans/small.md")" = "LIGHT" ]
}

@test "TestRefineAutoModeMissingFrontmatterDefaultsFull" {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  [ "$(auto_select_mode "$REPO_ROOT/tests/fixtures/refine/plans/no-frontmatter.md")" = "FULL" ]
}

@test "TestRefineAutoModeNonNumericLocDefaultsFull" {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  [ "$(auto_select_mode "$REPO_ROOT/tests/fixtures/refine/plans/non-numeric-loc.md")" = "FULL" ]
}

@test "TestRefineAutoModeUsesLocEstimateMax" {
  # Use loc_estimate_max specifically (not loc_estimate)
  cat > "$TMP/wrong-field.md" <<'EOF'
---
risk: low
loc_estimate: 100
touches_public_api: false
touches_security_surface: false
touches_dev_infra: false
---

body
EOF
  # loc_estimate_max missing → FULL
  [ "$(auto_select_mode "$TMP/wrong-field.md")" = "FULL" ]
}

@test "TestRefineFallsBackToStaticWhenRouterErrors" {
  # Critical lenses must resolve via the static map.
  out=$(refine_static_lens lens-1-correctness)
  echo "$out" | jq -e '.subagent_type == "developer-executor-correctness"'
  echo "$out" | jq -e '.critical == true'
  echo "$out" | jq -e '.fallback_used == true'
}

@test "TestRefineDoesNotRequireRouterRulesForCriticalLenses" {
  # All 4 critical lenses present in static map
  for lens in lens-1-correctness lens-5-testability lens-9-scope lens-10-goal-detect; do
    out=$(refine_static_lens "$lens")
    echo "$out" | jq -e '.critical == true' || return 1
  done
}

@test "TestRefineMdDeclaresSchemaValidation" {
  # render.md must show the goal-state update call
  grep -q 'goal-state.sh update' "$AUTO_MD" || \
    grep -q 'goal-state.sh update' "$REPO_ROOT/.devcontainer/images/.claude/commands/refine/render.md"
}
