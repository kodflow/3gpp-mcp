---
name: comment
description: |
  Audit and fix code comments across the project or a specific path.
  Ensures all comments explain WHY (not WHAT), functions have proper docstrings
  with params/types/return, and language conventions are respected.
  Dispatches parallel Haiku workers per file for speed.
model: opus
allowed-tools:
  - "Read(**/*)"
  - "Glob(**/*)"
  - "Grep(**/*)"
  - "Edit(**/*)"
  - "Bash(*)"
  - "Agent(*)"
---

# /comment - Code Comment Auditor

$ARGUMENTS

## Parse Arguments

Parse the input arguments:
- **No args**: audit the entire project (`/workspace/src/` or `/workspace/`)
- **Path** (first positional): audit a specific file or directory
- **`--check`**: dry-run mode, report issues without modifying files
- **`--lang <lang1,lang2>`**: filter by language (go, python, js, ts, java, rust, c, cpp, ruby, php, etc.)

Examples:
```
/comment                          # Audit entire project
/comment src/api/                 # Audit src/api/ directory
/comment src/main.go              # Audit single file
/comment --check                  # Dry-run, report only
/comment --check src/             # Dry-run on src/
/comment --lang go,python         # Only Go and Python files
/comment --lang rust --check      # Dry-run on Rust files only
```

## Execution

### Step 1: Validate Target

1. If path argument given, verify it exists with `Bash: ls`
2. If no path, default to `/workspace/src/` (fall back to `/workspace/` if src/ missing)
3. Parse `--check` and `--lang` flags

### Step 2: Delegate to Orchestrator

Invoke the `developer-commentator` agent via the **Agent** tool with the following prompt:

```
Audit code comments with the following parameters:
- Target: {resolved_path}
- Mode: {fix|check}
- Language filter: {languages or "all"}

Follow your full 3-phase workflow:
1. Scan & classify files
2. Dispatch workers in parallel
3. Validate and produce final report
```

### Step 3: Present Results

Display the orchestrator's final report to the user. If `--check` mode:
- Show issues found without modifications
- Suggest running without `--check` to apply fixes

If fix mode:
- Show summary of changes applied
- Show `git diff --stat` for verification
- Remind user to review changes before committing

## Notes

- The orchestrator spawns `developer-commentator-worker` (Haiku) agents in parallel
- Each worker handles one file and returns structured JSON
- Workers respect language-specific conventions (GoDoc, JSDoc, Sphinx, Javadoc, etc.)
- Trivial functions (getters, setters, simple constructors) are skipped
- Comments that already explain WHY correctly are preserved unchanged
- Use `/comment --check` in CI pipelines for comment quality gates

## PR7 — Skills Architecture v1.3: file-extension routing

The orchestrator routes each file to a `developer-commentator-worker`
via `route-agent.sh --skill /comment --phase audit-<ext>`. The router's
`agent_template` mechanism expands per detected language (Go, Python,
Rust, TS, …) so language-specific docstring conventions are enforced
by a worker that actually knows the convention — not a generic worker
falling back to "good enough" rules.

```bash
ROUTER=~/.claude/scripts/route-agent.sh
# Per-file dispatch (orchestrator iterates over files)
DISPATCH=$(bash "$ROUTER" --skill /comment --phase audit-go \
  --profile .claude/state/profile.json)
```
