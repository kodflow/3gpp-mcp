# Phase 3.0: Interactive Questions (IF NO PLAN)

**Ask these 4 questions ONLY if no approved plan is detected:**

> **Goal-turn skip rule (PR1 — Skills Architecture v1.3)**:
> if `mode == "GOAL_TURN"` (set by Phase 1.5 plan-detect), skip this
> entire phase. The goal-state file already encodes everything that
> would otherwise be asked here — re-prompting would block automated
> turn-by-turn execution.

## Question 1: Task Type

```yaml
AskUserQuestion:
  questions:
    - question: "What type of task do you want to accomplish?"
      header: "Type"
      multiSelect: false
      options:
        - label: "Refactor/Migration (Recommended)"
          description: "Migrate a framework, refactor existing code"
        - label: "Test Coverage"
          description: "Add tests to reach a coverage threshold"
        - label: "Standardization"
          description: "Apply consistent patterns (errors, style)"
        - label: "Greenfield"
          description: "Create a new project/module from scratch"
```

## Question 2: Max Iterations

```yaml
AskUserQuestion:
  questions:
    - question: "How many maximum iterations to allow?"
      header: "Iterations"
      multiSelect: false
      options:
        - label: "10 (Recommended)"
          description: "Sufficient for most tasks"
        - label: "20"
          description: "For moderately complex tasks"
        - label: "30"
          description: "For major migrations/refactorings"
        - label: "50"
          description: "For complete greenfield projects"
```

## Question 3: Success Criteria

```yaml
AskUserQuestion:
  questions:
    - question: "Which success criteria to use?"
      header: "Criteria"
      multiSelect: true
      options:
        - label: "Tests pass (Recommended)"
          description: "All unit tests must be green"
        - label: "Clean lint"
          description: "No linter errors"
        - label: "Build succeeds"
          description: "Compilation must work"
        - label: "Coverage >= X%"
          description: "Coverage threshold to reach"
```

## Question 4: Scope

```yaml
AskUserQuestion:
  questions:
    - question: "What scope for this task?"
      header: "Scope"
      multiSelect: false
      options:
        - label: "src/ folder (Recommended)"
          description: "All source code"
        - label: "Specific files"
          description: "I will specify the files"
        - label: "Entire project"
          description: "Includes tests, docs, config"
        - label: "Custom"
          description: "I will specify a path"
```

---

## Phase 4.0: Peek (RLM Pattern)

**Quick scan BEFORE any modification:**

```yaml
peek_workflow:
  0_git_check:
    action: "Check git status (conflict detection)"
    tools: [Bash]
    command: "git status --porcelain"
    checks:
      - "No merge/rebase in progress"
      - "Target files not already modified (warning if so)"
    on_conflict:
      action: "Warning + continue (not blocking)"
      message: "⚠ Uncommitted changes detected on target files"

  1_structure:
    action: "Scan the scope structure"
    tools: [Glob]
    patterns:
      - "src/**/*.{ts,js,go,py,rs}"
      - "tests/**/*"
      - "package.json | go.mod | Cargo.toml | pyproject.toml"

  2_patterns:
    action: "Identify existing patterns"
    tools: [Grep]
    searches:
      - "class.*Factory" → Factory pattern
      - "getInstance" → Singleton
      - "describe|test|it" → Existing tests

  3_stack_detect:
    action: "Detect the tech stack"
    checks:
      - "package.json → Node.js/npm"
      - "go.mod → Go"
      - "Cargo.toml → Rust"
      - "pyproject.toml → Python"
    output: "test_command, lint_command, build_command"
```

**Output Phase 4.0:**

```
═══════════════════════════════════════════════════════════════
  /do - Peek Analysis
═══════════════════════════════════════════════════════════════

  Git Status:
    ✓ Working tree clean (or: ⚠ 3 uncommitted changes)

  Scope      : src/
  Files      : 47 source files, 23 test files
  Stack      : Node.js (TypeScript)

  Patterns detected:
    ✓ Factory pattern (3 occurrences)
    ✓ Repository pattern (2 occurrences)
    ✓ Jest test suite (23 files)

  Commands:
    Test  : npm test
    Lint  : npm run lint
    Build : npm run build

═══════════════════════════════════════════════════════════════
```
