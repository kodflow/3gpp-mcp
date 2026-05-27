---
name: do
description: |
  Iterative task execution loop with RLM decomposition.
  Transforms a task into a persistent loop with automatic iteration.
  The agent keeps going, fixing its own mistakes, until success criteria are met.
  Also executes approved plans from /plan (auto-detected).
allowed-tools:
  - "Read(**/*)"
  - "Glob(**/*)"
  - "mcp__context7__*"
  - "Grep(**/*)"
  - "Write(**/*)"
  - "Edit(**/*)"
  - "Bash(*)"
  - "Task(*)"
  - "TaskCreate(*)"
  - "TaskUpdate(*)"
  - "TaskList(*)"
  - "TaskGet(*)"
  - "AskUserQuestion(*)"
  - "Skill(*)"
  - "PushNotification(*)"
---

# /do - Iterative Task Loop (RLM Architecture)

$ARGUMENTS

## CONTEXT7 (RECOMMENDED)

Use `mcp__context7__resolve-library-id` + `mcp__context7__query-docs` to:
- Verify library API usage before writing implementation code
- Check framework conventions when working on unfamiliar codebases
- Resolve ambiguous patterns by consulting up-to-date documentation

---

## Overview

Iterative loop using **Recursive Language Model** decomposition:

- **Peek** - Quick scan before execution
- **Decompose** - Split the task into sub-objectives
- **Parallelize** - Parallel validations (test, lint, build)
- **Synthesize** - Consolidated report

**Principle**: Iterate until success rather than aiming for perfection.

---

## Arguments

| Pattern | Action |
|---------|--------|
| `<task>` | Launch the interactive workflow |
| _(empty)_ | Execute the approved plan (if exists) |
| `--plan <path>` | Execute a specific plan file |
| `--goal-turn <slug>` | Resume a goal-state turn (skips questions + worktree confirm) — Skills Architecture v1.3 / PR1 |
| `--help` | Display help |

### Goal-turn mode (PR1)

`--goal-turn <slug>` reads `.claude/state/goals/<slug>.json` (managed by
`goal-state.sh`) and runs ONE iteration of the loop without asking
clarifying questions. State persists across invocations so a sequence of
`/do --goal-turn <slug>` calls reaches the goal incrementally.

```bash
# State CRUD helper
bash ~/.claude/scripts/goal-state.sh create <slug> <plan_path> <context_path>
bash ~/.claude/scripts/goal-state.sh update <slug> --iteration N --decision met
bash ~/.claude/scripts/goal-state.sh mark-stale <slug>   # safety net for orphaned sessions
bash ~/.claude/scripts/goal-state.sh gc                  # purge completed/abandoned > 7d
```

If the state file is missing, `--goal-turn` aborts with exit 4. If
`status` is `completed` or `abandoned`, it aborts with exit 5. If
`iteration >= max_iterations`, it aborts with exit 6.

---

## --help

```
═══════════════════════════════════════════════════════════════
  /do - Iterative Task Loop (RLM)
═══════════════════════════════════════════════════════════════

  DESCRIPTION
    Transforms a task into a persistent loop of iterations.
    The agent continues until the success criteria are met
    or the iteration limit is reached.

    If an approved plan exists (via /plan), it executes it
    automatically without asking interactive questions.

  USAGE
    /do <task>              Launch the interactive workflow
    /do                     Execute the approved plan (if exists)
    /do --plan <path>       Execute a specific plan file
    /do --help              Display this help

  RLM PATTERNS
    1. Plan    - Approved plan detection (skip questions if yes)
    2. Secret   - 1Password secret discovery
    3. Questions - Interactive configuration (if no plan)
    4. Peek     - Codebase scan + git conflict check
    5. Decompose - Split into measurable sub-objectives
    6. Loop     - Simultaneous validations (test/lint/build)
    7. Synthesize - Consolidated report per iteration

  EXAMPLES
    /do "Migrate Jest tests to Vitest"
    /do "Add tests to cover src/utils at 80%"
    /do                     # Execute the plan from /plan

  GUARDRAILS
    - Max 50 iterations (default: 10)
    - MEASURABLE success criteria only
    - Mandatory diff review before merge
    - Git conflict check before modifications

═══════════════════════════════════════════════════════════════
```

**IF `$ARGUMENTS` contains `--help`**: Display the help above and STOP.

---

## Phase Reference

| Phase | Module | Description |
|-------|--------|-------------|
| 1.0-2.0 | Read ~/.claude/commands/do/plan-detect.md | Plan detection + secret discovery |
| 3.0-4.0 | Read ~/.claude/commands/do/questions.md | Interactive questions (if no plan) + Peek |
| 5.0 | Read ~/.claude/commands/do/decompose.md | Task decomposition into sub-objectives |
| 5.5 | Read ~/.claude/commands/do/worktree.md | Worktree dispatch (optional, parallel) |
| 6.0 | Read ~/.claude/commands/do/loop.md | Main execution loop + guardrails |
| 7.0 | Read ~/.claude/commands/do/synthesis.md | Final report + skill integration |

---

## Execution Mode Detection (Agent Teams)

@.devcontainer/images/.claude/commands/shared/team-mode.md

Before Phase 5.5 (worktree dispatch), determine runtime mode:

```bash
source "$HOME/.claude/scripts/team-mode-primitives.sh"
MODE=$(detect_runtime_mode)
```

Branch:
- `TEAMS_TMUX` / `TEAMS_INPROCESS` → **TEAMS dispatch** using the plan's Parallelization table
- `SUBAGENTS` → legacy worktree dispatch in `do/worktree.md` + `do/loop.md` (unchanged)

### TEAMS dispatch

Lead: `developer-orchestrator`. For each worktree-tagged step in the approved plan's Parallelization table, spawn a teammate via the step's `Lead Agent` column (typically a `developer-specialist-<lang>`). Max 5 teammates per wave (hard cap from `shared/team-mode.md`).

Each spawned task carries an embedded task-contract v1 block:

```text
access_mode: "write"
owned_paths: <files from plan parallelization table for this step>
forbidden_paths: <files owned by sibling steps>
acceptance_criteria: <Step's "Verify" column>
output_format: "diff"
assignee: <teammate name from plan>
```

`task-created.sh` enforces 0 write collisions via the owned_paths check. Lead waits for all teammates, then runs Phase 7.0 synthesis. Token ceiling ≤ 2.5x legacy.

---

## Execution Flow

```
Phase 1.0: Plan Detection
  → Plan found? → PLAN_EXECUTION mode (skip questions)
  → No plan?   → ITERATIVE mode

Phase 2.0: Secret Discovery (1Password, non-blocking)

Phase 3.0: Interactive Questions (IF NO PLAN)
  → Task type, iterations, criteria, scope

Phase 4.0: Peek (RLM Pattern)
  → Git check, structure scan, pattern detection, stack detection

Phase 5.0: Decompose (RLM Pattern)
  → Split task into ordered sub-objectives

Phase 5.5: Worktree Dispatch (optional)
  → Only if plan has worktree=yes steps

Phase 6.0: Main Loop
  → Peek → Apply → Parallelize (test/lint/build) → Synthesize → Decision

Phase 7.0: Final Synthesis
  → Success or Failure report
```

---

## Rationalization Prevention (MANDATORY)

| Excuse You Might Think | Reality |
|------------------------|---------|
| "Issue is simple, don't need process" | Simple issues have root causes too. Process is fast. |
| "Emergency, no time for process" | Systematic is FASTER than guess-and-check thrashing. |
| "Just try this first, then investigate" | First fix sets the pattern. Do it right from start. |
| "I see the problem, let me fix it" | Seeing symptoms ≠ understanding root cause. |
| "One more fix attempt (after 2+)" | 3+ failures = architectural problem. STOP and escalate. |
| "Should work now" | Run verification. Evidence before claims. |
| "I'm confident it's fixed" | Confidence ≠ evidence. Run the tests. |

## 3-Fix Escalation Rule

```text
IF 3+ fix attempts have failed on the same issue:
  → STOP fixing immediately
  → This signals an ARCHITECTURAL problem, not an implementation problem
  → Question the approach, not the implementation
  → Escalate to user with AskUserQuestion explaining what was tried
```

## Verification Before Completion

```yaml
verification_gate:
  rule: "NO completion claims without fresh verification evidence"
  forbidden_phrases:
    - "Should work now"
    - "Probably fixed"
    - "Looks correct"
    - "Seems to work"
  required: "Run verification command, read FULL output, confirm with evidence"
  format: "[Run command] [See: output] → 'Verified: [claim]'"
  example:
    good: "[Run: make test] [See: 34/34 pass] → 'Verified: all tests pass'"
    bad: "'Should pass now' / 'Looks correct' / 'I'm confident'"
```

## Quick Guardrails

| Action | Status |
|--------|--------|
| Skip plan detection | **FORBIDDEN** |
| Skip questions without plan | **FORBIDDEN** |
| Skip peek | **FORBIDDEN** |
| Ignore max_iterations | **FORBIDDEN** |
| Subjective criteria | **FORBIDDEN** |
| Modify .claude/ or .devcontainer/ | **FORBIDDEN** |
| More than 50 iterations | **FORBIDDEN** |
| Claims without verification evidence | **FORBIDDEN** |
| 3+ fix attempts without escalation | **FORBIDDEN** |
