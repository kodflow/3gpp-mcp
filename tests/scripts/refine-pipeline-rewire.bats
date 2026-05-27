#!/usr/bin/env bats
# refine-pipeline-rewire.bats — Skills Architecture v1.6 (amendment)
# WHY: lock the post-rewire contract introduced by
# `feat/refine-pipeline-rewire-and-kill-do`:
#   1. /refine no longer auto-chains into /do (no Skill(skill=do) at all)
#   2. The 10 mono-concern refine-* agents are enumerated in dispatch.md
#   3. 4000 = ceiling, not target; floor_warn = 800 doctrine present
#   4. /refine emits a textual "Suggested next step: /goal <slug>"
#   5. /do is NOT advertised as deprecated — the skill stays functional
#      but is not surfaced in the user-facing skill tables
#   6. /refine emits a single predictable square-prompt directive with
#      7 mandatory sections; ACCEPTANCE and VERIFY are paired 1:1;
#      vague verbs ("fix", "improve") are rejected at synthesis time
# These invariants are the canonical regression net for any future
# refactor of the refine pipeline.

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  REFINE_MD="$REPO_ROOT/.devcontainer/images/.claude/commands/refine.md"
  DISPATCH="$REPO_ROOT/.devcontainer/images/.claude/commands/refine/dispatch.md"
  SYNTH="$REPO_ROOT/.devcontainer/images/.claude/commands/refine/synthesis.md"
  RENDER="$REPO_ROOT/.devcontainer/images/.claude/commands/refine/render.md"
  DO_MD="$REPO_ROOT/.devcontainer/images/.claude/commands/do.md"
  ROUTER="$REPO_ROOT/.devcontainer/images/.claude/scripts/route-agent.sh"
  TABLE="$REPO_ROOT/.devcontainer/images/.claude/agents/routing-table.jsonl"
  FALLBACK="$REPO_ROOT/.devcontainer/images/.claude/scripts/refine-static-fallback.sh"
  REGISTRY="$REPO_ROOT/.devcontainer/images/.claude/agents/registry.json"
  GO_PROFILE="$REPO_ROOT/tests/fixtures/profiles/go.json"
  TMP="$(mktemp -d)"
  export CLAUDE_AGENTS_DIR="$REPO_ROOT/.devcontainer/images/.claude/agents"
  export CLAUDE_ROUTING_TABLE="$TABLE"
  export CLAUDE_ROUTING_TELEMETRY="$TMP/router-fallbacks.jsonl"
}
teardown() { rm -rf "$TMP"; }

# Canonical phase list — every test that loops over the refine-* pipeline
# uses this list so a missed phase shows up as a hard test failure.
REFINE_PIPELINE_PHASES=(
  refine-content-pruner
  refine-scope-fencer
  refine-constraint-distiller
  refine-done-criteria-sharpener
  refine-verifier-binder
  refine-escalation-isolator
  refine-sequence-causal-validator
  refine-imperative-rewriter
  refine-chain-stripper
  refine-density-optimizer
)

# -- Invariant 1: no auto-chain from /refine to /do -----------------------

@test "TestRefineNoLongerAutoChainsToDo" {
  # Both spellings of the legacy chain call are gone from refine.md
  ! grep -q 'Skill(skill="do"' "$REFINE_MD"
  ! grep -q 'Skill(skill=do)' "$REFINE_MD"
}

# -- Invariant 2: allowed-tools no longer permits Skill(skill=do) ----------

@test "TestRefineAllowedToolsDoesNotIncludeSkillDo" {
  # The allowed-tools list entry is removed in v1.6 — without this, the
  # harness would silently accept a /refine → /do auto-chain again.
  ! grep -qE '^[[:space:]]*-[[:space:]]+"Skill\(skill=do\)"' "$REFINE_MD"
}

# -- Invariant 3: the 10 mono-concern refine-* agents are listed ----------

@test "TestRefineDispatchListsAllTenAgents" {
  # All 10 refine-* agents appear in dispatch.md, in canonical order.
  # If any one is missing the post-lens pipeline drops a concern.
  for agent in refine-content-pruner refine-scope-fencer \
               refine-constraint-distiller refine-done-criteria-sharpener \
               refine-verifier-binder refine-escalation-isolator \
               refine-sequence-causal-validator refine-imperative-rewriter \
               refine-chain-stripper refine-density-optimizer; do
    grep -q "$agent" "$DISPATCH" || { echo "missing agent: $agent"; return 1; }
  done
}

# -- Invariant 4: refine-density-optimizer is documented as terminal -------

@test "TestRefineDensityOptimizerIsTerminal" {
  # Pipeline causality: density compression MUST run last so it does not
  # destroy the structure later mono-concern agents would need to read.
  grep -qE 'refine-density-optimizer.*(last|Final)|MUST run last' "$DISPATCH"
}

# -- Invariant 5: synthesis uses ceiling semantics, not target ------------

@test "TestSynthesisUsesCeilingSemantics" {
  # 4000 is the CEILING (hard tool limit), not the design target. At
  # least 2 mentions enforce the doctrine across the file (block header
  # + narrative + schema doc).
  count=$(grep -cE '(ceiling|≤ ?4000)' "$SYNTH")
  [ "$count" -ge 2 ]
}

# -- Invariant 6: floor warning at 800 chars ------------------------------

@test "TestSynthesisHasFloorWarning" {
  # v1.6 amendment: emit a `suspect-over-compression` warning when the
  # rendered directive falls below 800 chars. Without the floor the
  # density optimizer could silently nuke half the contract.
  grep -q 'floor_warn = 800' "$SYNTH"
  grep -q 'minimum viable length' "$SYNTH"
}

# -- Invariant 7: render emits the textual suggestion line -----------------

@test "TestRenderEmitsSuggestedNextStep" {
  # The single hand-off after `/refine` is the printed suggestion line.
  # No Skill() call, no auto-chain — the user types `/goal <slug>`.
  count=$(grep -c 'Suggested next step' "$RENDER")
  [ "$count" -ge 1 ]
  ! grep -q 'Skill(skill="do"' "$RENDER"
}

# -- Invariant 8: /do is NOT advertised as deprecated --------------------

@test "TestDoMdHasNoDeprecationBanner" {
  # /do remains a working skill; we do not advertise it in the skill
  # tables and we do NOT label it deprecated. If users find it, it
  # works; if they don't, /goal <slug> is the documented path.
  ! grep -q '^## DEPRECATED' "$DO_MD"
}

# -- Invariant 9: render.md defines the square-prompt template -----------

@test "TestRenderDefinesSquarePromptTemplate" {
  # All 7 canonical sections must appear in the template definition.
  for section in '# CONTEXT' '# OBJECTIVE' '# SCOPE' \
                 '# CONSTRAINTS' '# ACCEPTANCE' '# VERIFY' '# STOP'; do
    grep -q "$section" "$RENDER" || { echo "missing section: $section"; return 1; }
  done
}

# -- Invariant 10: synthesis enforces square-prompt validation -----------

@test "TestSynthesisEnforcesSquarePromptValidation" {
  # The validator step is named and called out from the pipeline tables.
  grep -q 'square-prompt-validate' "$SYNTH"
  # Vague-verb rejection table is present (catches "fix ca", "improve", etc.)
  grep -q 'Vague-verb rejection table' "$SYNTH"
  grep -q 'user-must-fill-acceptance' "$SYNTH"
}

# -- Invariant 11: ACCEPTANCE and VERIFY are 1:1 paired ------------------

@test "TestSquarePromptAcceptanceVerifyPaired" {
  # render.md documents the 1:1 rule between ACCEPTANCE checkboxes and
  # VERIFY entries — the contract that makes "fix ca" impossible.
  grep -qE '1:1 mapping|one per ACCEPTANCE' "$RENDER"
}

# -- Invariant 12: route-agent resolves every refine-* pipeline phase ----

@test "TestRouteAgentResolvesAllRefinePipelinePhases" {
  # The reviewer's key blocker: dispatch.md documents 10 refine-* agents
  # but the router must actually find them. Each phase must resolve to a
  # concrete subagent_type (not general-purpose) with fallback_used=false.
  for phase in "${REFINE_PIPELINE_PHASES[@]}"; do
    run bash "$ROUTER" --skill /refine --phase "$phase" --profile "$GO_PROFILE"
    [ "$status" -eq 0 ] || { echo "router exit $status for $phase"; return 1; }
    echo "$output" | jq -e --arg p "$phase" '.[0].matched_rule_id != "no-match"' \
      || { echo "no rule matched for $phase"; return 1; }
    echo "$output" | jq -e '.[0].subagent_type != "general-purpose"' \
      || { echo "phase $phase fell to general-purpose"; return 1; }
    echo "$output" | jq -e '.[0].fallback_used == false' \
      || { echo "phase $phase used fallback unexpectedly"; return 1; }
  done
}

# -- Invariant 13: static fallback covers every refine-* pipeline phase --

@test "TestStaticFallbackResolvesAllRefinePipelinePhases" {
  # When route-agent.sh exits 20-31, the pipeline must still resolve via
  # refine-static-fallback.sh::refine_static_pipeline_phase(). Otherwise
  # dispatch.md's "static fallback" claim is fiction.
  source "$FALLBACK"
  for phase in "${REFINE_PIPELINE_PHASES[@]}"; do
    run refine_static_pipeline_phase "$phase" json
    [ "$status" -eq 0 ] || { echo "fallback failed for $phase"; return 1; }
    echo "$output" | jq -e '.subagent_type != "general-purpose"' \
      || { echo "fallback $phase returned general-purpose"; return 1; }
    echo "$output" | jq -e '.fallback_used == true' \
      || { echo "fallback $phase did not flag fallback_used"; return 1; }
    echo "$output" | jq -e --arg p "$phase" '.matched_rule_id == "static:\($p)"' \
      || { echo "fallback $phase has wrong matched_rule_id"; return 1; }
  done
}

@test "TestStaticFallbackRejectsUnknownPhase" {
  source "$FALLBACK"
  run refine_static_pipeline_phase "refine-not-a-phase" json
  [ "$status" -ne 0 ]
}

@test "TestStaticFallbackRouterParityForPipelinePhases" {
  # The router-result and fallback-result must agree on subagent_type for
  # every pipeline phase, so a router crash never silently changes which
  # agent runs. Same agent + same effort, regardless of code path.
  source "$FALLBACK"
  for phase in "${REFINE_PIPELINE_PHASES[@]}"; do
    router_agent=$(bash "$ROUTER" --skill /refine --phase "$phase" --profile "$GO_PROFILE" \
      | jq -r '.[0].subagent_type')
    fallback_agent=$(refine_static_pipeline_phase "$phase" json \
      | jq -r '.subagent_type')
    [ "$router_agent" = "$fallback_agent" ] \
      || { echo "agent mismatch for $phase: router=$router_agent fallback=$fallback_agent"; return 1; }
  done
}

# -- Invariant 14: registry.json reflects the v1.6 chain ------------------

@test "TestRegistryReflectsV16Chain" {
  # The architecture registry must enumerate the new pipeline stages or
  # downstream consumers (audit, plan) see a stale picture.
  chain=$(jq -r '.routing.refine.chain' "$REGISTRY")
  [ -n "$chain" ] && [ "$chain" != "null" ]
  echo "$chain" | grep -qF 'refine-* post-lens'
  echo "$chain" | grep -qF 'square-prompt'
  echo "$chain" | grep -qF 'compact-to-minimum'
}

# -- Invariant 15: validation runs post-compaction ------------------------

@test "TestSquarePromptValidatesAfterCompaction" {
  # Order matters: synthesis.md must show square-prompt-validate AFTER
  # compact-to-minimum so a compaction pass cannot silently break the
  # ACCEPTANCE/VERIFY mapping or the literal STOP block.
  grep -q 'compact-to-minimum → square-prompt-validate' "$SYNTH"
  grep -q 'validate twice\|validates twice\|TWICE' "$SYNTH"
}
