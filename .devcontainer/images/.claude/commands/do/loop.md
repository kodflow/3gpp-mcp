# Phase 6.0: Main Loop

```
┌──────────────────────────────────────────────────────────────┐
│  LOOP: while (iteration < max && !success)                   │
│                                                              │
│    1. Peek  → Read current state                             │
│    2. Apply → Minimal modifications                          │
│    3. Parallelize → Simultaneous validations                 │
│    4. Synthesize → Analyze results                           │
│    5. Decision → SUCCESS | CONTINUE | ABORT                  │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

## Step 3.1: Iterative Peek

```yaml
peek_iteration:
  action: "Read current state before modification"
  inputs:
    - "Previously modified files"
    - "Errors from last validation"
    - "Progress toward sub-objectives"
```

## Step 3.2: Apply (minimal modifications)

```yaml
apply_iteration:
  principle: "Smallest change that moves toward success"
  actions:
    - "Modify only the necessary files"
    - "Follow the project's existing patterns"
    - "Do not over-engineer"
  tracking:
    - "Add each modified file to the list"
```

## Step 3.3: Parallelize (simultaneous validations)

**Launch validations in PARALLEL via Task agents:**

```yaml
parallel_validation:
  agents:
    - task: "Run tests"
      command: "{test_command}"
      output: "test_result"

    - task: "Run linter"
      command: "{lint_command}"
      output: "lint_result"

    - task: "Run build"
      command: "{build_command}"
      output: "build_result"

  mode: "PARALLEL (single message, multiple Task calls)"
```

**IMPORTANT**: Launch all 3 validations in a SINGLE message.

## Step 3.4: Synthesize (result analysis)

```yaml
synthesize_iteration:
  collect:
    - "test_result.exit_code"
    - "test_result.passed / test_result.total"
    - "lint_result.error_count"
    - "build_result.exit_code"

  evaluate:
    all_success:
      condition: "test_exit == 0 && lint_exit == 0 && build_exit == 0"
      action: "EXIT with success report"

    partial_success:
      condition: "Some criteria met, some not"
      action: "CONTINUE with focused fixes"

    no_progress:
      condition: "Same errors 3 iterations in a row"
      action: "ABORT with blocker analysis"

  verification_gate:
    rule: "Evidence before claims. Previous runs don't count."
    mandatory_steps:
      1_IDENTIFY: "Which command proves the claim?"
      2_RUN: "Execute FRESHLY (no cache)"
      3_READ: "Lire output COMPLET + exit code"
      4_VERIFY: "Output confirme le claim?"
      5_CLAIM: "Seulement alors déclarer succès"
    red_flags:
      - "Hedging: 'should', 'probably', 'seems to'"
      - "Satisfaction before verification"
      - "Trust agent reports without independent verification"

  output: "Iteration summary"
```

**Output per iteration:**

```
═══════════════════════════════════════════════════════════════
  Iteration 3/10
═══════════════════════════════════════════════════════════════

  Modified: 5 files

  Validation (parallel):
    ├─ Tests : 18/23 PASS (5 failing)
    ├─ Lint  : 2 errors
    └─ Build : SUCCESS

  Analysis:
    - 5 tests use jest.mock() incompatible with vitest
    - 2 lint errors on unused imports

  Decision: CONTINUE → Focus on jest.mock migration

═══════════════════════════════════════════════════════════════
```

---

## Anti-patterns (Automatic Detection)

| Pattern | Symptom | Action |
|---------|---------|--------|
| **Circular fix** | Same file modified 3+ times | ABORT + alert |
| **No progress** | 0 improvement over 3 iterations | ABORT + diagnostic |
| **Scope creep** | Files outside scope modified | Rollback + warning |
| **Overbaking** | Inconsistent changes after 15+ iter | ABORT + report |
| **Architecture question** | 3+ failed fix attempts (same error category) | STOP + AskUserQuestion: "Is the architectural approach correct?" |

---

## TaskCreate Integration

```yaml
task_pattern:
  phase_0:
    - TaskCreate: { subject: "Configuration questions", activeForm: "Asking configuration questions" }
      → TaskUpdate: { status: "completed" }

  phase_1:
    - TaskCreate: { subject: "Peek: Analyze codebase", activeForm: "Analyzing codebase" }
      → TaskUpdate: { status: "in_progress" }

  phase_2:
    - TaskCreate: { subject: "{sub_objective_1}", activeForm: "Working on {sub_objective_1}" }
    - TaskCreate: { subject: "{sub_objective_2}", activeForm: "Working on {sub_objective_2}" }

  per_iteration:
    on_start: "TaskUpdate → status: in_progress"
    on_complete: "TaskUpdate → status: completed"
    on_blocked: "TaskCreate new blocker task"
    on_success: "TaskUpdate all → completed"
```

---

## Guardrails (ABSOLUTE)

| Action | Status | Reason |
|--------|--------|--------|
| Skip Phase 1.0 (Plan detect) | **FORBIDDEN** | Must check if plan exists |
| Skip Phase 3.0 without plan | **FORBIDDEN** | Questions required |
| Skip Phase 4.0 (Peek) | **FORBIDDEN** | Context + git check |
| Ignore max_iterations | **FORBIDDEN** | Infinite loop |
| Subjective criteria ("pretty", "clean") | **FORBIDDEN** | Not measurable |
| Modify .claude/ or .devcontainer/ | **FORBIDDEN** | Protected files |
| More than 50 iterations | **FORBIDDEN** | Safety limit |

### Legitimate Parallelization

| Element | Parallel? | Reason |
|---------|-----------|--------|
| Iterative loop (N → N+1) | Sequential | Iteration depends on previous result |
| Checks per iteration (lint+test+build) | Parallel | Independent of each other |
| Corrective actions | Sequential | Logical order required |

---

## Effective Prompt Examples

### Good: Measurable Criteria

```
/do "Migrate all Jest tests to Vitest"
→ Criterion: all tests pass with Vitest

/do "Add tests for src/utils with 80% coverage"
→ Criterion: coverage >= 80%

/do "Replace console.log with a structured logger"
→ Criterion: 0 console.log in src/, clean lint
```

### Bad: Subjective Criteria

```
/do "Make the code cleaner"
→ "Cleaner" is not measurable

/do "Improve performance"
→ No benchmark metric defined
```

---

## Terminal-state notification (PR5b — Skills Architecture v1.3)

When the loop reaches a terminal state, emit one `PushNotification` so
the user gets a desktop/mobile signal even if they walked away. Gated
by PR0's primitives.json — `absent` → fall back to stderr `[BELL]`.

```yaml
terminal_notify:
  trigger_states:
    - "success: all sub-objectives met"
    - "abort: 3-fix escalation triggered"
    - "max_iterations reached without success"

  primitive:
    PushNotification(
      message: "/do <slug> {{state}} after {{N}} iterations ({{duration}}s)",
      status: proactive
    )

  fallback_when_absent: |
    echo "[BELL] /do <slug> {{state}}" >&2
```
