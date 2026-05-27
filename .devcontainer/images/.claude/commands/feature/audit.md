# Phase 3: --checkup (Wave-Based Audit)

## Path Resolution (MANDATORY)

All `.claude/` paths MUST be absolute: `${WORKSPACE_ROOT}/.claude/` (resolve via `git rev-parse --show-toplevel || echo /workspace`).

---

```yaml
checkup_workflow:
  1_load_and_infer:
    action: "Read .claude/features.json, filter status != archived"
    infer: "Run infer_hierarchy → parent_map, children_map"
    output: "active_features[], waves (grouped by level)"

  2_determine_scope:
    if_id_provided: "Audit only the specified feature (single wave)"
    if_no_id: "Audit ALL active features (multi-wave)"

  3_compute_waves:
    action: |
      Group features by level: wave_0 = level 0, wave_1 = level 1, ...
      Execution order: wave 0 first, then wave 1, then wave 2, ...
      Max 8 parallel agents per wave.

  4_execute_waves:
    mode: "Sequential waves, parallel agents within each wave"
    per_wave:
      per_feature:
        # PR7 — Skills Architecture v1.3: replaces generic Explore with
        # docs-analyzer-* per audit aspect, routed via route-agent.sh
        # phase=audit-<aspect>. Falls back to docs-analyzer-architecture
        # when no specific aspect can be derived from the feature workdir.
        subagent_type: "docs-analyzer-architecture"
        model: "haiku"
        prompt: |
          Audit feature {id}: "{title}" [Level {level}]
          Description: {description}
          Status: {status}
          Workdirs: {workdirs}
          Audit dirs: {audit_dirs}
          Journal (last 5): {last_5_journal_entries}
          Parent audit result: {parent_result_json or "N/A (root)"}

          TASKS:
          1. Search codebase (Grep) for files in workdirs related to this feature
          2. Verify implementation matches description
          3. Identify gaps (described but not implemented)
          4. Identify possible improvements
          5. If parent result provided: check alignment with parent's standards
          6. Conformity score: PASS / PARTIAL / FAIL

          Return JSON:
          { "id": "...", "conformity": "...", "gaps": [], "improvements": [], "related_files": [] }

  5_auto_correction:
    trigger: "After each wave N completes"
    action: |
      FOR each child at wave N+1 with conformity PARTIAL or FAIL:
        parent = parent_map[child.id]
        IF parent exists AND parent.conformity == PASS:
          Generate auto-correction plan: .claude/plans/auto-correct-{child_id}.md
          Journal entry on child:
            { action: "auto_corrected", detail: "Parent {parent_id} generated correction plan" }
    constraint: "Direction is DOWNWARD ONLY. Each wave N corrects direct children at N+1; deeper descendants (N+2, N+3...) are corrected when their own wave runs. NEVER upward."

  6_cross_feature_analysis:
    action: "Analyze results across all waves for contradictions"
    checks:
      - "Two features modify same files conflictually"
      - "Feature depends on incomplete feature"
      - "Contradictory descriptions"
      - "Parent PASS but child FAIL (alignment gap)"

  7_generate_report:
    format: |
      ═══════════════════════════════════════════════════════════════
        /feature --checkup - Wave Audit Report
      ═══════════════════════════════════════════════════════════════

        Features audited: {n}
        Waves executed: {wave_count}

        Wave 0 (Level 0):
          ├─ F001: ✓ PASS (DDD Architecture)
          └─ F005: ✓ PASS (CI/CD Pipeline)

        Wave 1 (Level 1):
          ├─ F002: ⚠ PARTIAL (HTTP Server - 2 gaps)
          │  ↳ Auto-correction plan from parent F001
          └─ F003: ✓ PASS (Database layer)

        Wave 2 (Level 2):
          └─ F004: ✗ FAIL (Auth middleware)
             ↳ Auto-correction plan from parent F002

        Cross-feature:
          ├─ Contradiction: F002 vs F003 on data access pattern
          └─ Dependency: F004 blocked by F002

        Actions:
          → F002: /plan generated (.claude/plans/auto-correct-F002.md)
          → F004: /plan generated (.claude/plans/auto-correct-F004.md)

      ═══════════════════════════════════════════════════════════════

  8_update_journal:
    action: |
      For each audited feature:
        Add journal entry:
          { action: "checkup_pass"|"checkup_fail", detail: "Conformity: {score}, wave: {N}" }

  9_auto_plan:
    condition: "PARTIAL or FAIL or contradiction detected (non-auto-corrected)"
    action: |
      For each problem not already auto-corrected:
        Generate .claude/plans/fix-{feature_id}-{slug}.md
        Add journal entry: { action: "plan_generated", detail: "..." }
```
