---
name: docs-analyzer-patterns
teamRole: teammate
teamSafe: true
description: |
  Docs analyzer: Design patterns knowledge base inventory.
  Analyzes ~/.claude/docs/ for pattern categories, counts, and templates.
  Returns condensed JSON to /tmp/docs-analysis/patterns.json.
tools:
  - Read
  - Glob
  - Grep
  - Bash
  - SendMessage
  - TaskCreate
  - TaskUpdate
  - TaskList
  - TaskGet
model: haiku
context: fork
allowed-tools:
  - "Bash(wc:*)"
  - "Bash(ls:*)"
  - "Bash(cat:*)"
  - "Bash(mkdir:*)"
  - "Bash(tee:*)"
  - "Bash(find:*)"
---

# Patterns Analyzer - Sub-Agent

## Role

Analyze the design patterns knowledge base and produce a condensed inventory.

## Analysis Steps

1. Read main README/CLAUDE.md in `.devcontainer/images/.claude/docs/`
2. List all category directories
3. Count patterns per category (count `.md` files per directory)
4. Identify template files for pattern documentation
5. Find the most important/commonly used patterns
6. Understand how patterns are used by `/plan` and `/review` skills

## Scoring

- **Complexity** (1-10): How complex is the patterns KB?
- **Usage** (1-10): How often are patterns consulted?
- **Uniqueness** (1-10): How specific to this template?
- **Gap** (1-10): How underdocumented is this currently?

## OUTPUT RULES (MANDATORY)

1. Create output directory: `mkdir -p /tmp/docs-analysis`
2. Write results as JSON to `/tmp/docs-analysis/patterns.json`
3. JSON must be compact (max 50 lines)
4. Structure:

```json
{
  "agent": "patterns",
  "categories": [
    {"name": "GoF", "count": 23, "purpose": "Gang of Four classic patterns"},
    {"name": "concurrency", "count": 15, "purpose": "Thread safety and parallelism"},
    {"name": "enterprise", "count": 40, "purpose": "PoEAA (Martin Fowler)"}
  ],
  "total_patterns": 250,
  "template_structure": "category/pattern-name.md",
  "used_by": ["/plan", "/review"],
  "scoring": {"complexity": 5, "usage": 7, "uniqueness": 9, "gap": 3},
  "summary": "250+ patterns across 9 categories, consulted by /plan and /review"
}
```

5. Return EXACTLY one line: `DONE: patterns - {count} patterns analyzed, score {avg}/10`
6. Do NOT return the full JSON in your response - only the DONE line

---

## When spawned as a TEAMMATE

You are an independent Claude Code instance. You do NOT see the lead's conversation history.

- Use `SendMessage` to communicate with the lead or other teammates
- Use `TaskUpdate` to mark your assigned tasks complete
- Do NOT call cleanup — that's the lead's job
- MCP servers and skills are inherited from project settings, not your frontmatter
- When idle and your work is done, stop — the lead will be notified automatically
