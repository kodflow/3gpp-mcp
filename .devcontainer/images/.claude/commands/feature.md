---
name: feature
description: |
  Feature tracking with RTM (Requirements Traceability Matrix).
  CRUD operations, auto-learn from code changes, parallel audit.
allowed-tools:
  - "Read(**/*)"
  - "Write(.claude/**)"
  - "Edit(.claude/**)"
  - "Glob(**/*)"
  - "Grep(**/*)"
  - "Bash(jq:*)"
  - "Bash(mkdir:*)"
  - "Bash(cp:*)"
  - "Bash(mv:*)"
  - "Bash(date:*)"
  - "Bash(wc:*)"
  - "Task(*)"
  - "TaskCreate(*)"
  - "TaskUpdate(*)"
  - "TaskList(*)"
  - "TaskGet(*)"
  - "AskUserQuestion(*)"
---

# /feature - Feature Tracking RTM (Requirements Traceability Matrix)

$ARGUMENTS

---

## Overview

Track project features with full traceability: CRUD, audit, auto-learn.

**Database:** `.claude/features.json` (git-committed, no secrets)

---

## --help

```text
═══════════════════════════════════════════════════════════════
  /feature - Feature Tracking RTM
═══════════════════════════════════════════════════════════════

  DESCRIPTION
    Manage project features with full traceability.
    Hierarchical CRUD, wave-based audit, auto-learn from code.

  USAGE
    /feature --add "title" --desc "..." [--level N] [--workdirs "..."] [--audit-dirs "..."]
    /feature --edit <id> [--title "..."] [--desc "..."] [--status ...] [--level N] [--workdirs "..."] [--audit-dirs "..."]
    /feature --del <id>                           Delete (confirm)
    /feature --list                               Hierarchy tree
    /feature --show <id>                          Detail + journal
    /feature --checkup                            Wave audit ALL
    /feature --checkup <id>                       Audit one feature
    /feature --help                               This help

  STATUSES
    pending | in_progress | completed | blocked | archived

  LEVELS
    Level 0 : Top-level architectural features (e.g., DDD Architecture)
    Level 1 : Major subsystem features (e.g., HTTP Server, Database)
    Level 2+: Specific components (e.g., Auth middleware, Query builder)

    Parent-child: inferred at runtime from level + audit_dirs overlap.
    Direction: corrections flow DOWNWARD only (parent → child).

  DATABASE
    .claude/features.json (git-committed)
    Schema version: 2

  EXAMPLES
    /feature --add "DDD Architecture" --desc "Domain-driven design" --level 0 --workdirs "src/domain/,src/infrastructure/" --audit-dirs "src/"
    /feature --add "HTTP Server" --desc "REST API layer" --level 1 --workdirs "src/api/"
    /feature --edit F001 --status completed
    /feature --list
    /feature --checkup

═══════════════════════════════════════════════════════════════
```

**IF `$ARGUMENTS` contains `--help`**: Display the help above and STOP.

---

## Arguments

| Pattern | Action |
|---------|--------|
| `--add "title"` | Create new feature |
| `--edit <id>` | Modify existing feature |
| `--del <id>` | Delete feature (with confirmation) |
| `--list` | Display hierarchy tree |
| `--show <id>` | Feature detail + journal |
| `--checkup` | Wave audit ALL features |
| `--checkup <id>` | Audit single feature |
| `--help` | Display help |

---

## Phase Reference

| Phase | Module | Description |
|-------|--------|-------------|
| 1-2 | Read ~/.claude/commands/feature/crud.md | Init/migration + CRUD operations (add/edit/del/list/show) + hierarchy inference |
| 3 | Read ~/.claude/commands/feature/audit.md | --checkup wave-based audit with auto-correction |
| Ref | Read ~/.claude/commands/feature/autolearn.md | Journal actions, compaction, schema |

---

## Execution Flow

```
Phase 1: Init + Migration
  → Ensure .claude/features.json exists (v2)
  → Migrate v1 → v2 if needed

Phase 2: CRUD Operations
  → --add   : Generate ID, create entry, infer parent
  → --edit  : Update fields, journal entry, compact if needed
  → --del   : Confirm, delete or archive (cascade option)
  → --list  : Infer hierarchy, render tree
  → --show  : Full detail + journal

Phase 3: --checkup (Wave-Based Audit)
  → Group by level into waves
  → Execute waves sequentially (parallel agents per wave)
  → Auto-correction plans (downward only)
  → Cross-feature analysis
  → Generate report + fix plans
```

---

## Guardrails

| Action | Status | Reason |
|--------|--------|--------|
| Delete without confirmation | **FORBIDDEN** | Must use AskUserQuestion |
| Journal > 50 entries | **AUTO-COMPACT** | Keeps DB manageable |
| features.json > 500 features | **WARNING** | Consider archiving |
| Modify features.json schema | **FORBIDDEN** | Version migration needed |
| Store secrets in features.json | **FORBIDDEN** | Git-committed file |
| Auto-correct upward (child → parent) | **FORBIDDEN** | Corrections flow downward only |
| Delete parent with children | **WARN** | Offer cascade archive option |
| Level > 5 | **FORBIDDEN** | Reject input (must be <= 5) |
| Workdirs empty | **FORBIDDEN** | Reject input (required for hierarchy inference) |

## PR8 — `--gh-sync` (Skills Architecture v1.3)

Mirrors the RTM (`features.json`) to GitHub Issues + sub-issues so the
external backlog stays in sync with the internal hierarchy.

```bash
/feature --gh-sync               # one-shot full sync
/feature --gh-sync --dry-run     # report diff without writing
```

Implementation uses `mcp__github__issue_write` for create/update and
`mcp__github__sub_issue_write` to mirror parent/child links. Title and
labels are derived from `features.json`; the local file remains the
source of truth.
