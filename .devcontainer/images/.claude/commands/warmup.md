---
name: warmup
description: |
  Project context pre-loading with RLM decomposition.
  Reads CLAUDE.md hierarchy using funnel strategy (root → leaves).
  Use when: starting a session, preparing for complex tasks, or updating documentation.
allowed-tools:
  - "Read(**/*)"
  - "Glob(**/*)"
  - "mcp__context7__*"
  - "Grep(**/*)"
  - "Write(**/*)"
  - "Edit(**/*)"
  - "Task(*)"
  - "TaskCreate(*)"
  - "TaskUpdate(*)"
  - "TaskList(*)"
  - "TaskGet(*)"
  - "Bash(git:*)"
---

# /warmup - Project Context Pre-loading (RLM Architecture)

$ARGUMENTS

## CONTEXT7 (RECOMMENDED)

Use `mcp__context7__resolve-library-id` + `mcp__context7__query-docs` to:
- Validate CLAUDE.md conventions against current library documentation
- Check for outdated API references in existing documentation

---

## Overview

Project context pre-loading with **RLM** patterns:

- **Peek** - Discover the CLAUDE.md hierarchy
- **Funnel** - Funnel reading (root → leaves)
- **Parallelize** - Parallel analysis by domain
- **Synthesize** - Consolidated context ready to use

**Principle**: Load context → Be more effective on tasks

---

## Arguments

| Pattern | Action |
|---------|--------|
| (none) | Pre-load all project context |
| `--update` | Update all CLAUDE.md + create missing ones |
| `--dry-run` | Show what would be updated (with --update) |
| `--help` | Display help |

---

## --help

```
═══════════════════════════════════════════════════════════════
  /warmup - Project Context Pre-loading (RLM)
═══════════════════════════════════════════════════════════════

Usage: /warmup [options]

Options:
  (none)            Pre-load complete context
  --update          Update + create missing CLAUDE.md
  --dry-run         Show changes (with --update)
  --help            Display this help

Line Thresholds (CLAUDE.md):
  IDEAL       :   0-150 lines (simple directories)
  ACCEPTABLE  : 151-200 lines (medium complexity)
  WARNING     : 201-250 lines (review recommended)
  CRITICAL    : 251-300 lines (must be condensed)
  FORBIDDEN   :  301+ lines (split required)

Exclusions (STRICT .gitignore respect):
  - vendor/, node_modules/, .git/
  - All patterns from .gitignore are honored
  - bin/, dist/, build/ (generated outputs)

RLM Patterns:
  1. Peek       - Discover the CLAUDE.md hierarchy
  2. Funnel     - Funnel reading (root → leaves)
  3. Parallelize - Analysis by domain
  4. Synthesize - Consolidated context

Examples:
  /warmup                       Pre-load context
  /warmup --update              Update + create missing
  /warmup --update --dry-run    Preview changes

Workflow:
  /warmup → /plan → /do → /git

═══════════════════════════════════════════════════════════════
```

**IF `$ARGUMENTS` contains `--help`**: Display the help above and STOP.

---

## Quick Reference (Phase Dispatch)

### Normal Mode (Pre-loading)

| Phase | Action | Module |
|-------|--------|--------|
| 1.0 | Peek (hierarchy discovery + project detection) | Read ~/.claude/commands/warmup/scan.md |
| 1.5 | Feature context (conditional) | Read ~/.claude/commands/warmup/scan.md |
| 2.0 | Funnel reading (root → leaves) | Read ~/.claude/commands/warmup/read.md |
| 3.0 | Parallelize (4 agents: source, config, test, docs) | Read ~/.claude/commands/warmup/read.md |
| 4.0 | Synthesize (consolidated context) | Read ~/.claude/commands/warmup/read.md |

### Update Mode (--update)

| Phase | Action | Module |
|-------|--------|--------|
| 1.0 | Full code scan (respecting .gitignore) | Read ~/.claude/commands/warmup/update.md |
| 2.0 | Create missing CLAUDE.md files | Read ~/.claude/commands/warmup/update.md |
| 3.0 | Obsolescence detection | Read ~/.claude/commands/warmup/update.md |
| 4.0 | Generate updates | Read ~/.claude/commands/warmup/update.md |
| 5.0 | Apply changes (interactive or dry-run) | Read ~/.claude/commands/warmup/update.md |
| 7.0 | Learn (extract conventions) | Read ~/.claude/commands/warmup/update.md |

**To execute a phase**, read the corresponding module file for full instructions.

---

## Guardrails (ABSOLUTE)

| Action | Status | Reason |
|--------|--------|--------|
| Skip Phase 1 (Peek) | **FORBIDDEN** | Hierarchy discovery is MANDATORY |
| Modify .claude/commands/ | **FORBIDDEN** | Protected files |
| Delete CLAUDE.md | **FORBIDDEN** | Only updates allowed |
| Ignore .gitignore | **FORBIDDEN** | Source of truth for exclusions |
| Create CLAUDE.md in gitignored dir | **FORBIDDEN** | vendor/, node_modules/, etc. |
| CLAUDE.md > 300 lines | **FORBIDDEN** | Must be split |
| CLAUDE.md 251-300 lines | **CRITICAL** | Condensation MANDATORY |
| CLAUDE.md 201-250 lines | **WARNING** | Review recommended |
| Random reading | **FORBIDDEN** | Funnel (root→leaves) MANDATORY |
| Implementation details | **FORBIDDEN** | Context, not code |

**CLAUDE.md line thresholds:**

```
┌────────────┬─────────┬───────────────────────────────────────┐
│   Level    │ Lines   │             Action                    │
├────────────┼─────────┼───────────────────────────────────────┤
│ IDEAL      │ 0-150   │ No action needed                      │
├────────────┼─────────┼───────────────────────────────────────┤
│ ACCEPTABLE │ 151-200 │ Medium directory, acceptable           │
├────────────┼─────────┼───────────────────────────────────────┤
│ WARNING    │ 201-250 │ Review recommended at next pass        │
├────────────┼─────────┼───────────────────────────────────────┤
│ CRITICAL   │ 251-300 │ Condensation MANDATORY                 │
├────────────┼─────────┼───────────────────────────────────────┤
│ FORBIDDEN  │ 301+    │ Must be split or restructured          │
└────────────┴─────────┴───────────────────────────────────────┘
```

---

## Workflow Integration

```
/warmup                     # Pre-load context
    ↓
/plan "feature X"           # Plan with context
    ↓
/do                         # Execute the plan
    ↓
/warmup --update            # Update documentation
    ↓
/git --commit               # Commit changes
```

**Integration with other skills:**

| Before /warmup | After /warmup |
|----------------|---------------|
| Container start | /plan, /review, /do |
| /init | Any complex task |

---

## Design Patterns Applied

| Pattern | Category | Usage |
|---------|----------|-------|
| Cache-Aside | Cloud | Check cache before loading |
| Lazy Loading | Performance | Load by phases (funnel) |
| Progressive Disclosure | DevOps | Increasing detail by depth |

**References:**
- `~/.claude/docs/cloud/cache-aside.md`
- `~/.claude/docs/performance/lazy-load.md`
- `~/.claude/docs/devops/feature-toggles.md`
