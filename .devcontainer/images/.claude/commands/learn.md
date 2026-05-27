---
name: learn
description: Extract reusable patterns from the current session and save them to the local knowledge base.
allowed-tools: ["Read", "Grep", "Glob", "Write", "Edit", "Bash(git log:*)", "Bash(wc:*)", "Bash(tail:*)", "Bash(date:*)", "Bash(mkdir:*)", "AskUserQuestion"]
model: sonnet
---

# /learn — Extract Reusable Patterns

## Overview

Analyze the current session to identify and save reusable patterns to `~/.claude/docs/learned/`.

## Arguments

| Pattern | Action |
|---------|--------|
| (no args) | Analyze session and propose patterns |
| `<description>` | Save a specific pattern with given description |
| `--list` | List all learned patterns |
| `--status` | Show learning statistics |

---

## Workflow

### Phase 1: Gather Session Data

```yaml
sources:
  primary: "/workspace/.claude/logs/<branch>/session.jsonl"
  fallback: "git log --oneline -20"
  context: "Current conversation context (corrections, fixes, workarounds)"

gather:
  1_get_branch: "git rev-parse --abbrev-ref HEAD"
  2_read_log: "tail -100 /workspace/.claude/logs/<branch>/session.jsonl"
  3_if_no_log: "Use conversation context directly"
```

### Phase 2: Identify Extractable Patterns

**Look for these pattern types (priority order):**

| Type | Detection Signal | Value |
|------|-----------------|-------|
| **User corrections** | "No, use X instead", "don't do that", undo → redo | HIGH |
| **Error resolutions** | Error → investigation → fix sequence | HIGH |
| **Workarounds** | Library quirks, API limitations, version-specific fixes | MEDIUM |
| **Debugging techniques** | Non-obvious diagnostic steps, tool combinations | MEDIUM |
| **Project conventions** | Codebase patterns discovered during exploration | LOW |

**Filter OUT (do NOT extract):**
- Trivial fixes (typos, simple syntax errors)
- One-time issues (specific API outages, transient errors)
- Patterns already in `~/.claude/docs/` (check for duplicates)
- Generic knowledge (things any developer would know)

### Phase 3: Draft Pattern

**Generate pattern file with this format:**

```markdown
---
name: <kebab-case-name>
category: learned
extracted: <ISO8601>
confidence: <0.5-0.9 based on evidence strength>
trigger: "<when this pattern should be applied>"
source: "<session|conversation|git-history>"
---
# <Descriptive Pattern Name>

## Problem
<What problem this solves — be specific, include error messages if applicable>

## Solution
<The technique, workaround, or pattern>

## Example
<Code example if applicable — BAD/GOOD side-by-side preferred>

## When to Use
<Trigger conditions — what should activate this pattern>

## Evidence
<How this was discovered — brief context>
```

### Phase 4: Validate & Save

```yaml
validation:
  1_check_duplicate:
    action: "Grep ~/.claude/docs/ for similar patterns"
    if_duplicate: "Propose updating existing pattern instead"

  2_ask_user:
    action: "AskUserQuestion with pattern preview"
    options:
      - "Save as-is"
      - "Edit before saving"
      - "Skip (not useful enough)"

  3_save:
    path: "~/.claude/docs/learned/<name>.md"
    create_dir: "mkdir -p ~/.claude/docs/learned/"

  4_update_index:
    action: "Append entry to ~/.claude/docs/README.md under Learned Patterns section"
    format: "| <name> | <1-line description> | learned/<name>.md |"
```

### Phase 5: Report

```text
═══════════════════════════════════════════════
  /learn — Pattern Extracted
═══════════════════════════════════════════════

  Pattern  : <name>
  Category : learned
  Confidence: <0.X>
  Trigger  : <when to apply>

  Saved to : ~/.claude/docs/learned/<name>.md
  Index    : ~/.claude/docs/README.md (updated)

═══════════════════════════════════════════════
```

---

## --list

```yaml
list_workflow:
  action: "Glob ~/.claude/docs/learned/*.md"
  output: |
    ═══════════════════════════════════════════════
      /learn — Learned Patterns
    ═══════════════════════════════════════════════

      Patterns in ~/.claude/docs/learned/:
        ├─ <name>.md (confidence: 0.8, extracted: 2026-03-15)
        └─ <name>.md (confidence: 0.6, extracted: 2026-03-10)

      Total: N patterns

    ═══════════════════════════════════════════════
```

---

## --status

Show learning statistics:
- Total patterns extracted
- Patterns by confidence level
- Most recent extractions
- Session log size (observations available)

---

## Guardrails

| Action | Status |
|--------|--------|
| Save without user confirmation | FORBIDDEN |
| Extract trivial patterns | FORBIDDEN |
| Duplicate existing patterns | FORBIDDEN |
| Save secrets or credentials | FORBIDDEN |
| Modify existing non-learned patterns | FORBIDDEN |

## PR8 — SessionEnd auto-propose (Skills Architecture v1.3)

```yaml
session_end_autopropose:
  trigger: "SessionEnd hook"
  condition: ">=3 user corrections detected in session.jsonl"
  action: |
    PushNotification(
      message: "/learn detected {{N}} corrections — run /learn to extract patterns",
      status: proactive
    )
  rate_limit: "once per session; do not nag"
  fallback_when_pushnotification_absent: "stderr [LEARN] hint"
```
