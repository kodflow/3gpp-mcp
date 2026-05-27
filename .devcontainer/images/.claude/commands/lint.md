---
name: lint
description: |
  Multi-language intelligent linting with RLM decomposition.
  Auto-detects project language(s) and dispatches to the appropriate workflow.
  Go projects with ktn-linter: 148 rules across 8 phases with Agent Teams.
  Other languages: lint-fix-iterate loop with language-specific tools.
  Makefile-first: uses `make lint` when available.
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
---

# /lint - Multi-Language Intelligent Linting (RLM Architecture)

$ARGUMENTS

## CONTEXT7 (RECOMMENDED)

Use `mcp__context7__resolve-library-id` + `mcp__context7__query-docs` to:
- Verify linter rule documentation when fixing complex violations
- Check framework-specific lint configurations

---

## AUTOMATIC WORKFLOW

This skill fixes **ALL** linting issues without exception.
No arguments. No flags. Auto-detects language and tools.

---

## Phase 0: Language Detection

**Run `detect-project.sh` to get languages, tools, and build system in ONE call:**

```bash
bash ~/.claude/scripts/detect-project.sh
```

This returns JSON with `languages[]`, `build_system.targets[]`, `tools{}`, `project_type`.
Use this output for ALL routing decisions below. DO NOT re-check markers individually.

**Fallback** (if script unavailable): scan the project root for build system markers:

```yaml
detection_order:
  - marker: "go.mod"
    language: "go"
    check_ktn: true  # Also check for ktn-linter binary

  - marker: "Cargo.toml"
    language: "rust"

  - marker: "package.json"
    language: "nodejs"
    sub_check: "tsconfig.json → typescript"

  - marker: "pyproject.toml OR setup.py OR setup.cfg OR requirements.txt"
    language: "python"

  - marker: "pom.xml OR build.gradle OR build.gradle.kts"
    language: "java"

  - marker: "*.csproj OR *.sln"
    language: "csharp"

  - marker: "Gemfile"
    language: "ruby"

  - marker: "composer.json"
    language: "php"

  - marker: "mix.exs"
    language: "elixir"

  - marker: "pubspec.yaml"
    language: "dart"

  - marker: "build.sbt"
    language: "scala"

  - marker: "Package.swift"
    language: "swift"

  - marker: "build.gradle.kts with kotlin"
    language: "kotlin"

  - marker: "fpm.toml"
    language: "fortran"

  - marker: "alire.toml"
    language: "ada"

  - marker: "CMakeLists.txt"
    language: "c_cpp"

  # Fallback: scan file extensions in src/ or project root
  - fallback: "extension scan"
    extensions:
      ".lua": "lua"
      ".pl,.pm": "perl"
      ".r,.R": "r"
      ".pas,.dpr": "pascal"
      ".vb": "vbnet"
      ".cob,.cbl": "cobol"
      ".f90,.f95,.f03,.f08": "fortran"
      ".adb,.ads": "ada"
      ".scala": "scala"
      ".kt,.kts": "kotlin"
```

**Result**: A list of detected languages (can be multiple for monorepos).

---

## Phase 1: Routing

### Makefile-first (any language)

```text
IF Makefile exists with "lint" target:
  → Run: make lint
  → Parse output, fix issues, re-run until convergence
  → DONE (skip language-specific dispatch)
```

### Go with ktn-linter

```text
IF "go" detected AND (ktn-linter binary exists OR cmd/ktn-linter/ dir exists):
  → Read ~/.claude/commands/lint/go.md
  → Execute full 8-phase ktn-linter workflow
```

### Go without ktn-linter

```text
IF "go" detected AND no ktn-linter:
  → Read ~/.claude/commands/lint/generic.md
  → Use golangci-lint fallback
```

### Other languages

```text
FOR each detected language:
  → Read ~/.claude/commands/lint/generic.md
  → Execute lint-fix-iterate with language-specific tools
```

### Multi-language (Agent Teams)

```text
IF multiple languages detected AND Agent Teams available:
  → Lead handles Phase 0 detection
  → Spawn one teammate per language (lint/generic.md each)
  → Wait for all teammates to complete
  → Synthesize combined report

IF multiple languages detected AND no Agent Teams:
  → Execute sequentially: primary language first, then secondary
```

---

## Module Reference

| Action | Module |
|--------|--------|
| Go ktn-linter gateway | Read ~/.claude/commands/lint/go.md |
| Generic lint-fix-iterate (18+ languages) | Read ~/.claude/commands/lint/generic.md |
| Go: 148 rules by phase | Read ~/.claude/commands/lint/rules.md |
| Go: Execution workflow & agent teams | Read ~/.claude/commands/lint/execution.md |
| Go: DTO convention & detection | Read ~/.claude/commands/lint/dto.md |

---

## Final Report

```text
═══════════════════════════════════════════════════════════════
  /lint - COMPLETE
═══════════════════════════════════════════════════════════════

  Languages detected : Go, TypeScript
  Mode               : Agent Teams (1 teammate per language)

  Go (ktn-linter 8-phase):
    Issues fixed     : 47
    Iterations       : 3
    DTOs detected    : 4

  TypeScript (eslint + tsc):
    Issues fixed     : 12
    Iterations       : 2
    Type errors      : 0

  Final verification : 0 issues across all languages

═══════════════════════════════════════════════════════════════
```

## PR8 — `--watch` mode (Skills Architecture v1.3)

Reacts to file changes via `inotifywait`. Streamed by the `Monitor`
primitive when present; falls back to polling otherwise.

```bash
/lint --watch                # watch src/ for relevant changes
/lint --watch --path src/api # narrower scope
```

Monitor invocation:

```
Monitor(
  description: "lint on file change",
  timeout_ms: 3600000,
  command: "inotifywait -m -r -e modify,create --exclude '\\.git/' src/ tests/ 2>&1 | grep --line-buffered -vE 'WATCHES_LIMIT|Setting up watches'"
)
```

For each event, run the language detector + corresponding linter; emit
a `PushNotification` only when a fresh issue appears (no spam on green).
