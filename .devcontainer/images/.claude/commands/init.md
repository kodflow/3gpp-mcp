---
name: init
description: |
  Conversational project discovery + doc generation.
  Open-ended dialogue builds rich context, then synthesizes all project docs.
  Use when: creating new project, starting work, verifying setup.
allowed-tools:
  - Write
  - Edit
  - "Bash(git:*)"
  - "Bash(docker:*)"
  - "Bash(terraform:*)"
  - "Bash(kubectl:*)"
  - "Bash(node:*)"
  - "Bash(python:*)"
  - "Bash(go:*)"
  - "Bash(curl:*)"
  - "Bash(pgrep:*)"
  - "Bash(nohup:*)"
  - "Bash(mkdir:*)"
  - "Bash(rm:*)"
  - "Bash(wc:*)"
  - "Read(**/*)"
  - "Glob(**/*)"
  - "mcp__context7__*"
  - "Grep(**/*)"
  - "Task(*)"
  - "TaskCreate(*)"
  - "TaskUpdate(*)"
  - "TaskList(*)"
  - "TaskGet(*)"
  - "mcp__github__*"
---

# /init - Conversational Project Discovery

$ARGUMENTS

## CONTEXT7 (RECOMMENDED)

Use `mcp__context7__resolve-library-id` + `mcp__context7__query-docs` to:
- Identify detected framework conventions and best practices
- Fetch current stable versions and recommended configurations

---

## Overview

Conversational initialization with **progressive context building**:

1. **Detect** - Template or already personalized?
2. **Discover** - Open-ended conversation to understand the project
3. **Synthesize** - Review accumulated context with user
4. **Generate** - Produce all project docs from rich context
5. **Validate** - Environment, tools, deps, config

---

## Usage

```
/init                # Everything automatic
```

**Intelligent behavior:**
- Detects template → starts discovery conversation
- Detects personalized → skips to validation
- Detects problems → auto-fix when possible
- No flags, no unnecessary questions

---

## Quick Reference (Phase Dispatch)

| Phase | Action | Module |
|-------|--------|--------|
| 1.0 | Repository detection (template vs personalized) | Read ~/.claude/commands/init/discovery.md |
| 2.0 | Conversational discovery (4-10 exchanges) | Read ~/.claude/commands/init/discovery.md |
| 3.0 | Vision synthesis (review with user) | Read ~/.claude/commands/init/discovery.md |
| 4.0 | File generation (vision, CLAUDE, AGENTS, etc.) | Read ~/.claude/commands/init/generate.md |
| 4.5 | CodeRabbit configuration | Read ~/.claude/commands/init/generate.md |
| 4.6 | Qodo Merge configuration | Read ~/.claude/commands/init/templates.md |
| 4.7 | GitHub branch protection (CI gates) | Read ~/.claude/commands/init/templates.md |
| 4.8 | Feature bootstrap | Read ~/.claude/commands/init/templates.md |
| 5.0 | Environment validation (parallel checks) | Read ~/.claude/commands/init/validate.md |
| 6.0 | Final report | Read ~/.claude/commands/init/validate.md |
| 7.0 | GrepAI calibration | Read ~/.claude/commands/init/validate.md |

**To execute a phase**, read the corresponding module file for full instructions.
