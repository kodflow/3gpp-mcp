# Phase 5.0: Synthesize (RLM Pattern)

## Phase 6.0: ExitPlanMode invocation (PR1 — Skills Architecture v1.3)

Once the plan is fully synthesized, `/plan` invokes `ExitPlanMode(plan=<full md>)`
instead of rendering the legacy ASCII banner. This hands control back to the
user for approval; the same flow drives `/do --plan <path>` thereafter.

**Schema validation (PR1 fix #18)**: before calling `ExitPlanMode`, read
`.claude/state/primitives.json` (emitted by PR0's `probe-primitives.sh`)
and confirm:

```bash
jq -e '.ExitPlanMode.status == "present"
       and (.ExitPlanMode.input_schema.properties.plan.type == "string")' \
   .claude/state/primitives.json
```

If the check fails:

1. Fall back to writing the plan to `${WORKSPACE_ROOT}/.claude/plans/<slug>.md` directly.
2. Print a `[plan] ExitPlanMode unavailable — wrote plan to <path>` notice.
3. Suggest `Skill(skill="do", args="--plan <path>")` as the manual hand-off.

After the call, if `--goal` flag was present, chain into `/refine` via
`Skill(skill="refine", args="<slug>")` (PR5a). Until W3 lands the refine
skill is absent — the call is a no-op and the plan is still usable by `/do`.



## Path Resolution (MANDATORY)

All `.claude/` paths MUST be absolute, anchored to workspace root:
```bash
WORKSPACE_ROOT=$(git rev-parse --show-toplevel 2>/dev/null || echo /workspace)
```
- Write plans to: `${WORKSPACE_ROOT}/.claude/plans/{slug}.md`
- Write contexts to: `${WORKSPACE_ROOT}/.claude/contexts/{slug}.md`
- Glob plans from: `${WORKSPACE_ROOT}/.claude/plans/*.md`

**NEVER use relative `.claude/` paths** — subagents may operate from subdirectories.

---

**Generate the structured plan:**

```yaml
synthesize_workflow:
  plan_audience:
    rule: "Plan must be executable by a skilled developer with ZERO domain knowledge"
    implications:
      - "Chemins de fichiers EXACTS (pas 'the auth module')"
      - "Code samples COMPLETS (pas 'implement the logic')"
      - "Commandes CLI EXACTES avec outputs attendus"

  step_granularity:
    rule: "Each step = 1 TDD cycle (2-5 min)"
    format: |
      ### Step N: <Titre>
      **Files:** `src/file.ts` (create), `tests/file.test.ts` (create)
      **Test first:** Write failing test for {behavior}
      **Implement:** Minimal code to pass
      **Verify:** Run tests, confirm green
      **Commit:** `feat(scope): description`

  1_collect:
    action: "Collect agent results"

  2_consolidate:
    action: "Merge into coherent plan"

  2.5_propose_approaches:
    rule: "Propose 2-3 approaches with trade-offs BEFORE picking one"
    trigger: "After consolidation, before plan generation — SKIP AskUserQuestion if --auto (pick best internally, document reasoning in plan)"
    format: |
      Present each approach with:
      - Name (e.g., "Minimal change", "Clean architecture", "Pragmatic balance")
      - Advantages (2-3 bullets)
      - Disadvantages (2-3 bullets)
      - Files touched (count)
      - Complexity (low/medium/high)
      - Your recommendation with reasoning
    tool: AskUserQuestion
    question: "Quelle approche preferes-tu ?"
    options_dynamic: true  # Options generated from analysis
    exception: "For trivial tasks (< 3 files), skip and use the obvious approach"

  3_generate:
    format: "Structured plan document based on CHOSEN approach"

  4_persist_to_disk:
    action: "Write plan to .claude/plans/{slug}.md"
    slug_rule: "Same as /search: lowercase, hyphens, max 40 chars from description"
    collision: "If file exists, append timestamp suffix (-YYYYMMDD-HHMM)"
    purpose: "Survives context compaction; /do can detect from disk"
    note: "This is IN ADDITION to ExitPlanMode (which shows plan to user)"

  5_persist_context:
    action: "Write context file to .claude/contexts/{slug}.md"
    trigger: "Always after plan generation"
    purpose: "Captures discoveries, relevant files, and implementation notes for /do recovery"
    content:
      header: |
        # Context: {description}
        Generated: {ISO8601}
        Plan: .claude/plans/{slug}.md
      sections:
        discoveries: "Key findings from codebase analysis (patterns, conventions, gotchas)"
        relevant_files: "Files examined during planning with brief role description"
        implementation_notes: "Technical decisions, trade-offs, constraints discovered"
        dependencies: "External libs, APIs, or services involved"
    link_in_plan:
      action: "Add 'Context: .claude/contexts/{slug}.md' line in plan header"
      format: |
        # Implementation Plan: {description}
        Context: .claude/contexts/{slug}.md
```

**Plan Output Format:**

```markdown
# Implementation Plan: <description>
Context: .claude/contexts/<slug>.md

## Overview
<2-3 sentences summarizing the approach>

## Design Patterns Applied

| Pattern | Category | Justification | Reference |
|---------|----------|---------------|-----------|
| Repository | DDD | Data access abstraction | ~/.claude/docs/ddd/README.md |
| Factory | Creational | Token creation | ~/.claude/docs/creational/README.md |

## Prerequisites
- [ ] <Required dependency or setup>
- [ ] <Other prerequisite>

## Implementation Steps

### Step 1: <Title>
**Files:** `src/file1.ts`, `src/file2.ts`
**Actions:**
1. <Specific action>
2. <Specific action>

**Code pattern:**
```<lang>
// Example of what will be implemented
```

### Step 2: <Title>
...

## Parallelization (optional — for multi-step plans)

When steps are independent (no shared files, no dependency), tag them for parallel execution:

| Step | Files | Agent | Model | Worktree | Depends On |
|------|-------|-------|-------|----------|------------|
| 1 | src/auth/ | developer-specialist-go | sonnet | yes | - |
| 2 | src/api/ | developer-specialist-go | haiku | yes | - |
| 3 | docs/ | developer-specialist-review | haiku | no | 1, 2 |

`/do` will use this table to create worktrees and dispatch agents in parallel (semi-auto: user confirms before creating worktrees).

**Rules:**
- Only tag `worktree: yes` if step touches DIFFERENT files than other parallel steps
- Assign the most specific agent for the task
- Model follows agent default (from registry.json)
- Steps with `depends_on` run sequentially AFTER dependencies complete

## Testing Strategy
- [ ] Unit tests for `component`
- [ ] Integration test for `flow`

## Rollback Plan
How to rollback if issues

## Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| Risk description | Solution |
```

---

## Phase 5.5: Complexity Check

**Triggered automatically after Synthesize. NOT a blocker -- just a question.**

```yaml
complexity_check:
  trigger: "files_to_modify + files_to_create > 15"

  action:
    tool: AskUserQuestion
    questions:
      - question: "This plan touches {n} files. Beyond ~15 files in a single session, quality may degrade. How do you want to proceed?"
        header: "Scope"
        options:
          - label: "Execute as-is"
            description: "Proceed with the full plan in one session"
          - label: "Split into phases"
            description: "Claude will propose logical segments to execute separately"

  on_execute_as_is:
    action: "Continue to Phase 6.0 normally"

  on_split:
    action: |
      Rewrite the plan into numbered phases (Phase A, B, C...)
      Each phase: <= 15 files, independently testable
      User approves each phase via /do
```

**If <= 15 files:** Skip this phase silently, proceed to Phase 6.0.

---

## Phase 5.9: Risk Review (INTERACTIVE CHECKPOINT)

**MANDATORY: Present risks before final validation.**

```yaml
risk_review:
  trigger: "Before Phase 6.0 — SKIP AskUserQuestion if --auto (assess risks internally, include in plan)"
  present: "Risks & Mitigations table from the generated plan"
  tool: AskUserQuestion
  question: "Voici les risques identifies. Sont-ils acceptables ?"
  options:
    - label: "Oui, genere le plan final"
      description: "Les risques sont acceptables, continuer"
    - label: "Ajouter des mitigations"
      description: "Je veux renforcer la gestion de certains risques"
    - label: "Changer d'approche"
      description: "Ces risques sont trop importants, revenir au choix d'approche"

  on_ajouter:
    action: "Ask: Quels risques renforcer ? Quelle mitigation ?"
    then: "Update plan risks table, re-present"

  on_changer:
    action: "Return to Phase 2.5 (propose approaches)"
```

---

## Phase 6.0: Validation Request

**MANDATORY: Wait for user approval**

```
═══════════════════════════════════════════════════════════════
  Plan ready for review
═══════════════════════════════════════════════════════════════

  Summary:
    • 4 implementation steps
    • 6 files to modify
    • 2 new files to create
    • 8 tests to add

  Design Patterns:
    • Repository (DDD)
    • Factory (Creational)

  Estimated complexity: MEDIUM

  Actions:
    → Review the plan above
    → Run /do to execute (auto-detects plan)
    → Or modify the plan manually

═══════════════════════════════════════════════════════════════
```

---

## Integration with Other Skills

| Before /plan | After /plan |
|-------------|-------------|
| `/search <topic>` | `/do` |
| Generates `.claude/contexts/{slug}.md` | Executes the plan (auto-detected from conversation or `.claude/plans/`) |

**Full workflow:**

```
/search "JWT authentication best practices"
    ↓
.claude/contexts/jwt-auth-best-practices.md generated
    ↓
/plan "Add JWT auth to API" --context
    ↓
Plan created, displayed, AND persisted to .claude/plans/add-jwt-auth-api.md
    ↓
User: "OK, go ahead"
    ↓
/do                          # Detects plan from conversation OR .claude/plans/
    ↓
Implementation executed
```

**Note**: `/do` automatically detects the approved plan from conversation context
or from `.claude/plans/*.md` on disk (conversation takes priority).
Plans persist across context compaction.
