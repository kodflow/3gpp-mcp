#!/usr/bin/env bats
# refine-v14-modes.bats — Skills Architecture v1.6
# WHY: lock the 3-mode contract (FULL / BARE / FROM-CONTRACT) plus the
# uniform 4000-char ceiling (not target) and auto-detection from disk
# state. The free-form-to-/goal path is the one users will reach for
# most often; it MUST keep working without a plan + context pair and
# without an explicit --bare flag.

setup() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  REFINE_MD="$REPO_ROOT/.devcontainer/images/.claude/commands/refine.md"
  REFINE_DIR="$REPO_ROOT/.devcontainer/images/.claude/commands/refine"
  SYNTH="$REFINE_DIR/synthesis.md"
  RENDER="$REFINE_DIR/render.md"
  AUTO_MD="$REFINE_DIR/auto.md"
  DISPATCH="$REFINE_DIR/dispatch.md"
}

# -- Mode declaration -----------------------------------------------------

@test "TestRefineDeclaresThreeModes" {
  grep -q 'FULL' "$REFINE_MD"
  grep -q '\-\-bare' "$REFINE_MD"
  grep -q '\-\-from-contract' "$REFINE_MD"
}

@test "TestRefineBareFlagInArgumentsTable" {
  grep -q '`--bare`' "$REFINE_MD"
}

@test "TestRefineFromContractFlagInArgumentsTable" {
  grep -qE '`--from-contract <slug>`' "$REFINE_MD"
}

# -- 4000-char ceiling (not target) ---------------------------------------

@test "TestRefineCeilingIs4000" {
  # v1.6 amendment: 4000 is the CEILING (hard tool limit), not the design
  # target. The design target is the minimum viable length.
  grep -q '4000-char ceiling' "$REFINE_MD"
  grep -qE '(ceiling|≤ ?4000)' "$SYNTH"
  grep -q 'ceiling   = 4000 chars' "$SYNTH"
}

@test "TestRefineHasFloorWarning" {
  # v1.6 amendment: floor_warn = 800 catches over-compression
  grep -q 'floor_warn = 800' "$SYNTH"
  grep -q 'suspect-over-compression' "$SYNTH"
}

@test "TestRefineNoDualBudget" {
  # No leftover LIGHT-budget split (would be a v1.4 regression)
  ! grep -E 'LIGHT.*2000|2000.*LIGHT|--full-budget' "$REFINE_MD"
  ! grep -E '2000 chars by default|4096' "$SYNTH"
  ! grep -E '4096' "$RENDER"
  ! grep -E '4096' "$AUTO_MD"
  ! grep -E '4096' "$DISPATCH"
}

@test "TestRefineSchemaUses4000Target" {
  grep -q '"directive_char_target": 4000' "$SYNTH"
  grep -q '"directive": "<≤4000 chars>"' "$SYNTH"
}

@test "TestRefineAllowsShorterOutput" {
  # WHY: 4000 is the ceiling, not a floor — natural shorter is fine.
  # The synthesis explicitly states it never pads to hit the ceiling.
  grep -qE 'never pads to hit (4000|the ceiling)' "$SYNTH"
  grep -q 'minimum viable length' "$SYNTH" \
    || grep -q 'minimum viable length' "$REFINE_MD"
}

# -- Auto-detection -------------------------------------------------------

@test "TestRefineAutoDetectsBareFromSpaces" {
  grep -q 'contains spaces' "$REFINE_MD"
  grep -q 'BARE (free-form description)' "$REFINE_MD"
}

@test "TestRefineAutoDetectsFullFromExistingFiles" {
  grep -q 'plans/<arg>.md + contexts/<arg>.md both exist' "$REFINE_MD"
  grep -qE '→ +FULL' "$REFINE_MD"
}

@test "TestRefineAutoDetectsFromContractWhenOnlyGoalExists" {
  grep -q 'goals/<arg>.md exists, no plan/context' "$REFINE_MD"
  grep -qE '→ +FROM-CONTRACT' "$REFINE_MD"
}

@test "TestRefineFlagsAreOverridesNotPrimary" {
  grep -q 'Explicit overrides for the edge cases' "$REFINE_MD"
  grep -q 'override the default detection' "$REFINE_MD"
}

# -- BARE pipeline --------------------------------------------------------

@test "TestRefineBareSkipsLensDispatch" {
  grep -q 'BARE.*skip\|skip.*BARE' "$DISPATCH"
  grep -q 'no proof triplets' "$RENDER"
}

@test "TestRefineBareAppliesWhatWhyWhereHowDoneTemplate" {
  grep -q 'WHAT/WHY/WHERE/HOW/DONE' "$REFINE_MD"
  grep -q 'WHAT  : ' "$SYNTH"
  grep -q 'WHY   : ' "$SYNTH"
  grep -q 'WHERE : ' "$SYNTH"
  grep -q 'HOW   : ' "$SYNTH"
  grep -q 'DONE  : ' "$SYNTH"
}

@test "TestRefineBareContractTemplateDeclared" {
  grep -q 'BARE contract template' "$RENDER"
  grep -q 'mode: BARE' "$RENDER"
}

# -- FROM-CONTRACT pipeline -----------------------------------------------

@test "TestRefineFromContractNeverOverwritesInput" {
  grep -q 'never overwritten' "$RENDER"
  grep -q 'contract_written.*false' "$SYNTH"
}

# -- AUTO lens depth (independent of char-cap) ----------------------------

@test "TestRefineAutoControlsOnlyLensDepth" {
  grep -q 'AUTO mode controls .*lens depth only' "$AUTO_MD"
  grep -q 'does NOT control the char-cap' "$AUTO_MD"
}

# -- Slug derivation ------------------------------------------------------

@test "TestRefineBareDerivesSlugDeterministically" {
  grep -q 'derive_slug' "$RENDER"
  grep -q 'fix-race-in-worker-go' "$RENDER"
}

@test "TestRefineSlugFlagOverridesDerivation" {
  grep -qE '`--slug <name>`' "$REFINE_MD"
}

# -- Synthesis pipeline integrity -----------------------------------------

@test "TestRefineSynthesisPipelineDiffersPerMode" {
  # v1.6 amendment: pipelines insert refine-pipeline + square-prompt-validate
  # between render and compact, and compact is renamed to compact-to-minimum
  # (ceiling, not target). All three modes keep distinct entry sequences.
  grep -q 'collect → dedup → rank → render → refine-pipeline → square-prompt-validate → compact-to-minimum' "$SYNTH"
  grep -q 'template → render → refine-pipeline → square-prompt-validate → compact-to-minimum' "$SYNTH"
  grep -q 'read → extract → render → refine-pipeline → square-prompt-validate → compact-to-minimum' "$SYNTH"
}

@test "TestRefineModeEnumInOutputSchema" {
  grep -q 'FULL|BARE|FROM_CONTRACT' "$SYNTH"
}

# -- Workflow narrative ---------------------------------------------------

@test "TestRefineQuickWorkflowSkipsPlanAndSearch" {
  # The whole point of auto-BARE per the user's question
  grep -q '/refine "fix race in worker.go pool init"' "$REFINE_MD"
  grep -q 'No explicit mode flag needed' "$REFINE_MD"
}
