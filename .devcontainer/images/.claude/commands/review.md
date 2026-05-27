---
name: review
description: |
  AI-powered code review (RLM decomposition) for PRs/MRs or local diffs.
  Focus: correctness, security, design, quality, shell safety.
  15 phases, 3 tiers: T1 (5 agents), T2 (Qodo), T3 (CodeRabbit).
  Cyclic workflow: /review --loop for iterative perfection.
  Local-only output with /plan generation for /do execution.
allowed-tools:
  - "Bash(git *)"
  - "Bash(gh *)"
  - "Bash(glab *)"
  - "Read(**/*)"
  - "Glob(**/*)"
  - "Grep(**/*)"
  - "mcp__github__*"
  - "mcp__gitlab__*"

  - "mcp__context7__*"
  - "Task(*)"
  - "TaskCreate(*)"
  - "TaskUpdate(*)"
  - "TaskList(*)"
  - "TaskGet(*)"
  - "Bash(qodo run review *)"
  - "Bash(coderabbit review *)"
---

# Review - AI Code Review (RLM Architecture)

$ARGUMENTS

## CONTEXT7 (RECOMMENDED)

Use `mcp__context7__resolve-library-id` + `mcp__context7__query-docs` to verify:
- Library API usage correctness (design/correctness executors)
- Security best practices for frameworks (security executor)
- Deprecated API detection (quality executor)

---

## Overview

Intelligent code review using **Recursive Language Model** decomposition:

| Phase | Name | Action |
|-------|------|--------|
| 0 | Context | Detect PR/MR, branch, CI status (GitHub/GitLab) |
| 0.5 | **Repo Profile** | Cache conventions, architecture, ownership (7d TTL) |
| 1 | Intent | Analyze PR/MR + **Risk Model** calibration |
| 1.5 | Describe | Auto-generate PR/MR description (drift detection) |
| 2 | Feedback | Collect ALL comments/reviews |
| 2.3 | **CI Diagnostics** | Extract CI failure context (conditional) |
| 2.5 | Questions | Handle human questions |
| 3 | Peek | Snapshot diff, categorize files, route agents |
| 4 | **Analyze** | **5 parallel agents + external tiers** (T1: agents, T2: Qodo, T3: CodeRabbit) |
| 4.7 | **Merge & Dedupe** | Normalize findings, deduplicate, require evidence |
| 5 | Challenge | Evaluate feedback relevance with context |
| 6 | Output | Generate LOCAL report + /plan file (no GitHub/GitLab post) |
| 6.5 | **Dispatch** | Route fixes to language-specialist via /do |
| 7 | **Cyclic** | Loop until perfect OR --loop limit |

**RLM Principle:** Peek → Decompose → Parallelize → Synthesize

**3-Tier Review Architecture:**

| Tier | Source | Execution | Output |
|------|--------|-----------|--------|
| T1 (Internal) | 5 Claude agents (opus+haiku) | Task subagents | JSON findings |
| T2 (Qodo) | `qodo run review` | Bash `--ci --silent --log` | Text → parsed |
| T3 (CodeRabbit) | `coderabbit review --plain` | Bash `--plain --base` | Text → parsed |

**T1 Agents (opus/sonnet for reasoning, haiku for patterns):**
- `developer-executor-correctness` (sonnet) - Algorithmic errors, invariants, silent failures
- `developer-executor-security` (opus) - Taint analysis, OWASP, supply chain
- `developer-executor-design` (sonnet) - Antipatterns, DDD, layering, SOLID
- `developer-executor-quality` (haiku) - Style, complexity, metrics
- `developer-executor-shell` (haiku) - Shell safety, Dockerfile, CI/CD

**Confidence Scoring:** All agents score findings 0-100. Only findings above reporting threshold are reported (see triage.md: MEDIUM>=75, HIGH>=85, CRITICAL>=95).
**FP Filtering:** Pre-existing issues, non-modified lines, linter-catchable issues are excluded.
**Platform Support:** GitHub (PRs) + GitLab (MRs) - auto-detected from git remote.
**Output:** LOCAL only (no comments posted). Generates /plan for /do execution.

---

## Usage

```
/review                    # Single review (no loop, no fix)
/review --loop             # Cyclic review until PERFECT (infinite)
/review --loop 5           # Cyclic review (max 5 iterations)
/review --pr [number]      # Review specific PR (GitHub)
/review --mr [number]      # Review specific MR (GitLab)
/review --staged           # Review staged changes only
/review --file <path>      # Review specific file
/review --security         # Security-focused review only
/review --correctness      # Correctness-focused review only
/review --design           # Design/architecture review only
/review --quality          # Quality-focused review only
/review --triage           # Large PR/MR mode (>30 files or >1500 lines)
/review --describe         # Force auto-describe even if PR/MR has description
/review --tier all          # All tiers: T1+T2+T3 (default)
/review --tier internal     # T1 only (5 agents, no external)
/review --tier external     # T2+T3 only (qodo + coderabbit)
/review --tier qodo         # T2 only
/review --tier coderabbit   # T3 only
```

**Cyclic Workflow:**
```
/review --loop
    │
    ├── Phase 0-6: Full analysis (5 agents)
    ├── Phase 6.5: Generate /plan with fixes
    ├── /do executes fixes via language-specialist
    ├── Loop: re-review → fix → re-review
    └── Exit when: no HIGH/CRITICAL OR limit reached
```

---

## Budget Controller (MANDATORY)

```yaml
budget_controller:
  thresholds:
    normal_mode:
      max_files: 30
      max_lines: 1500
      max_comments_ingested: 80
    triage_mode:
      trigger: "files > 30 OR lines > 1500"
      action: "Focus on: unresolved threads, modified lines, security only"

  output_limits:
    critical: unlimited
    high: 10
    medium: 5
    low: 3

  comment_priority:
    1: "Unresolved threads"
    2: "Comments on modified lines"
    3: "Human reviews"
    4: "AI bot suggestions"
```

**Automatic decision:**

| Situation | Mode |
|-----------|------|
| diff < 1500 lines, files < 30 | NORMAL |
| diff >= 1500 OR files >= 30 | TRIAGE |
| comments > 80 | FILTER (unresolved + modified lines only) |

---

## Quick Reference (Phase Dispatch)

| Phase | Action | Module |
|-------|--------|--------|
| 0-0.5 | Context detection, repo profile | Read ~/.claude/commands/review/dispatch.md |
| 1-1.5 | Intent analysis, auto-describe | Read ~/.claude/commands/review/dispatch.md |
| 2-2.5 | Feedback collection, CI, questions | Read ~/.claude/commands/review/triage.md |
| 3 | Peek & decompose (diff snapshot) | Read ~/.claude/commands/review/dispatch.md |
| 4 | Parallel analysis (5 agents) | Read ~/.claude/commands/review/triage.md |
| 4.7 | Merge & dedupe (normalize findings) | Read ~/.claude/commands/review/triage.md |
| 5 | Challenge & synthesize | Read ~/.claude/commands/review/synthesis.md |
| 6 | Output generation (LOCAL report) | Read ~/.claude/commands/review/synthesis.md |
| 6.5 | Language-specialist dispatch | Read ~/.claude/commands/review/cyclic.md |
| 7 | Cyclic validation (--loop) | Read ~/.claude/commands/review/cyclic.md |
| T2+T3 | Qodo + CodeRabbit integration | Read ~/.claude/commands/review/tiers.md |

**To execute a phase**, read the corresponding module file for full instructions.

---

## Execution Mode Detection (Agent Teams)

@.devcontainer/images/.claude/commands/shared/team-mode.md

Before Phase 4 (parallel analysis), determine the runtime mode via the canonical block from section 3 of `shared/team-mode.md`:

```bash
source "$HOME/.claude/scripts/team-mode-primitives.sh"
MODE=$(detect_runtime_mode)
```

Branch on `$MODE`:
- `TEAMS_TMUX` or `TEAMS_INPROCESS` → Phase 4 dispatches via **Agent Teams** (5 parallel teammates, see triage.md §10 TEAMS execution)
- `SUBAGENTS` → Phase 4 dispatches via the legacy `Task` tool path (see triage.md §10 SUBAGENTS execution — unchanged from today)

Both paths MUST be functionally, schema-, and semantically equivalent. No new user-visible sections in the SUBAGENTS fallback.

---

## Success Criteria (Agent Teams migration)

- **Functional equivalence**: same set of findings across modes on a given PR (set comparison, not ordered)
- **Schema equivalence**: synthesis report has identical sections, severity levels, finding fields
- **Semantic equivalence**: a "critical" in legacy mode is still "critical" in TEAMS mode
- **Fallback invariant**: `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=0 /review` runs the SUBAGENTS path unchanged
- **Performance floor** (TEAMS_TMUX): ≥ 1.5x faster wall-clock on a 30+ file PR
- **Pilot token ceiling**: ≤ 2.5x legacy cost (B1-specific, not a universal norm)
- **Stability**: 5 consecutive runs without manual intervention
- **Hook cleanliness**: 0 false-positive TaskCreated rejections across 10 runs

---

## --help

```text
═══════════════════════════════════════════════════════════════
  /review - AI Code Review (RLM Architecture)
═══════════════════════════════════════════════════════════════

Usage: /review [options]

Options:
  (none)            Single review (no loop)
  --loop [N]        Cyclic review (infinite or max N)
  --pr [number]     Review specific PR (GitHub)
  --mr [number]     Review specific MR (GitLab)
  --staged          Review staged changes only
  --file <path>     Review specific file
  --security        Security-focused review only
  --correctness     Correctness-focused only
  --design          Design/architecture only
  --quality         Quality-focused only
  --triage          Large PR/MR mode
  --describe        Force auto-describe
  --tier <tier>     all|internal|external|qodo|coderabbit
  --help            Display this help

Tiers:
  T1 (Internal)   5 Claude agents (correctness, security, design, quality, shell)
  T2 (Qodo)       qodo run review (P0/P1/P2 triage)
  T3 (CodeRabbit) coderabbit review --plain

Modes:
  NORMAL    diff < 1500 lines, files < 30
  TRIAGE    diff >= 1500 OR files >= 30

Cyclic Workflow:
  /review --loop → review → fix → review → ... → PERFECT
  Exit: no HIGH/CRITICAL OR limit reached OR Ctrl+C

Output:
  LOCAL only (no PR/MR comments posted)
  Generates .claude/plans/review-fixes-{timestamp}.md
  Run /do to apply fixes

Workflow:
  /review → /do (apply fixes) → /git --commit

═══════════════════════════════════════════════════════════════
```

**IF `$ARGUMENTS` contains `--help`**: Display the help above and STOP.

---

## Guardrails (Quick Reference)

| Action | Status |
|--------|--------|
| Auto-approve/merge | FORBIDDEN |
| Skip security issues | FORBIDDEN |
| Modify code directly | FORBIDDEN |
| Post comment without user validation | FORBIDDEN |
| Mention AI in PR responses | **ABSOLUTE FORBIDDEN** |
| Expose secrets in evidence/output | FORBIDDEN |
| Ignore budget limits | FORBIDDEN |
