# RLM Execution Workflow (Go / ktn-linter)

> This module is Go-specific. It is invoked from lint/go.md.
> For other languages, see lint/generic.md.

## IMMEDIATE EXECUTION

### Step 1: Run ktn-linter

```bash
./builds/ktn-linter lint ./... 2>&1
```

If the binary does not exist:

```bash
go build -o ./builds/ktn-linter ./cmd/ktn-linter && ./builds/ktn-linter lint ./...
```

### Step 2: Parse the output

For each error line with format `file:line:column: KTN-XXX-YYY: message`:

1. Extract the file
2. Extract the rule (KTN-XXX-YYY)
3. Extract the message
4. Classify into the appropriate phase

### Step 3: Classify by phase

Refer to Read ~/.claude/commands/lint/rules.md for rule-to-phase mapping.

---

## Execution Mode: Agent Teams (Claude 4.6)

**If `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` is enabled**, use Agent Teams to parallelize independent phases.

### Agent Teams Architecture

```text
LEAD (Phase 1-3: SEQUENTIAL - inter-file dependencies)
  Phase 1: STRUCTURAL (7 rules)
    ↓
  Phase 2: SIGNATURES (7 rules)
    ↓
  Phase 3: LOGIC (17 rules)
    ↓
  Re-run ktn-linter → validate Phase 1-3 convergence
    ↓
  === SPAWN 4 TEAMMATES ===
    ├── "perf"   → Phase 4 PERFORMANCE (11 rules)
    ├── "modern" → Phase 5 MODERN (20 rules)
    ├── "polish" → Phase 6 STYLE + Phase 7 DOCS (25 rules)
    └── "tester" → Phase 8 TESTS (8 rules)
    ↓
  LEAD: wait for all teammates to complete
    ↓
  Final re-run ktn-linter → validate global convergence
```

### Teammate Roles

**Lead**: Orchestrates the workflow. Executes Phase 1-3 (structural + signatures + logic) which have inter-file dependencies. After Phase 3 convergence, spawns the 4 teammates. Collects results and launches the final verification.

**Teammate "perf"** (Phase 4): Memory optimization specialist. Fixes: KTN-VAR-HOTLOOP, KTN-VAR-BIGSTRUCT, KTN-VAR-SLICECAP, KTN-VAR-MAPCAP, KTN-VAR-MAKEAPPEND, KTN-VAR-GROW, KTN-VAR-STRBUILDER, KTN-VAR-STRCONV, KTN-VAR-SYNCPOOL, KTN-VAR-ARRAY.

**Teammate "modern"** (Phase 5): Idiomatic Go specialist. Fixes: KTN-VAR-USEANY, KTN-VAR-USECLEAR, KTN-VAR-USEMINMAX, KTN-VAR-RANGEINT, KTN-VAR-LOOPVAR, KTN-VAR-SLICEGROW, KTN-VAR-SLICECLONE, KTN-VAR-MAPCLONE, KTN-VAR-CMPOR, KTN-VAR-WGGO, MODERNIZE-*.

**Teammate "polish"** (Phase 6+7): Style and documentation. Fixes: KTN-VAR-CAMEL, KTN-CONST-CAMEL, KTN-VAR-MINLEN/MAXLEN, KTN-FUNC-UNUSEDARG, KTN-FUNC-NOMAGIC, KTN-FUNC-EARLYRET, KTN-STRUCT-NOGET, KTN-INTERFACE-ERNAME + all KTN-COMMENT-*.

**Teammate "tester"** (Phase 8): Test quality. Fixes: KTN-TEST-TABLE, KTN-TEST-COVERAGE, KTN-TEST-ASSERT, KTN-TEST-ERRCASES, KTN-TEST-NOSKIP, KTN-TEST-SETENV, KTN-TEST-SUBPARALLEL, KTN-TEST-CLEANUP.

### User Interaction (VS Code)

- `Shift+Up/Down` to navigate between teammates
- Write directly to a teammate to guide its decisions
- Each teammate uses TaskCreate/TaskUpdate to report its progress

### Fallback: Sequential Mode

**If Agent Teams not available**, execute the classic mode:

```text
FOR each phase from 1 to 8:
    FOR each issue in this phase:
        1. Read the affected file
        2. IF struct DTO → apply dto:"dir,ctx,sec" convention
        3. Apply the fix
        4. TaskUpdate → completed
    END FOR
END FOR

Re-run ktn-linter to verify convergence
IF still issues: restart
ELSE: finish with report
```

---

## Final Report

```text
═══════════════════════════════════════════════════════════════
  /lint - COMPLETE
═══════════════════════════════════════════════════════════════

  Mode             : Agent Teams (4 teammates) | Sequential
  Issues fixed     : 47
  Iterations       : 3
  DTOs detected    : 4 (excluded from ONEFILE/CTOR)

  By phase:
    STRUCTURAL  : 5 fixed (including 2 via dto tags)  [Lead]
    SIGNATURES  : 8 fixed                              [Lead]
    LOGIC       : 12 fixed                             [Lead]
    PERFORMANCE : 4 fixed                              [perf]
    MODERN      : 10 fixed                             [modern]
    STYLE       : 5 fixed                              [polish]
    DOCS        : 3 fixed                              [polish]
    TESTS       : 0 fixed                              [tester]

  DTOs processed:
    - user_dto.go: CreateUserRequest, UserResponse (dto:"...,api,...")
    - order_dto.go: OrderCommand, OrderQuery (dto:"...,cmd/query,...")

  Final verification: 0 issues

═══════════════════════════════════════════════════════════════
```

---

## ABSOLUTE RULES

1. **Fix EVERYTHING** - No exceptions, no skips
2. **Phase ordering** - Phase 1→3 sequential, Phase 4→8 parallel (Agent Teams) or sequential (fallback)
3. **DTOs on-the-fly** - Detect and apply dto:"dir,ctx,sec"
4. **Iteration** - Re-run until 0 issues
5. **No questions** - Everything is automatic
6. **Strict dto format** - Always 3 values separated by comma
7. **TaskCreate** - Each phase = 1 task with progress

---

## START NOW

1. Run `./builds/ktn-linter lint ./...`
2. Parse the output
3. Classify by phase
4. Detect Agent Teams availability (`CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS`)
5. IF Agent Teams: Lead Phase 1-3, spawn teammates Phase 4-8
6. ELSE: fix sequentially 1→8 (DTOs with dto:"dir,ctx,sec" convention)
7. Re-run until convergence
8. Display final report
