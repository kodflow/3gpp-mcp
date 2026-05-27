---
name: git
description: |
  Workflow Git Automation with RLM decomposition.
  Handles branch management, conventional commits, and CI validation.
  Use when: committing changes, creating PRs/MRs, or merging with CI checks.
  Supports GitHub (PRs) and GitLab (MRs) - auto-detected from git remote.
allowed-tools:
  - "Bash(git:*)"
  - "Bash(gh:*)"
  - "Bash(glab:*)"
  - "mcp__github__*"
  - "mcp__gitlab__*"
  - "Read(**/*)"
  - "Write(.env)"
  - "Edit(.env)"
  - "Glob(**/*)"
  - "Grep(**/*)"
  - "Task(*)"
  - "TaskCreate(*)"
  - "TaskUpdate(*)"
  - "TaskList(*)"
  - "TaskGet(*)"
  - "AskUserQuestion(*)"
---

# /git - Workflow Git Automation (RLM Architecture)

## Modular Architecture

This skill is split into focused modules for maintainability.
**Read the relevant module BEFORE executing each phase.**

```
~/.claude/commands/git/
├── identity.md      # Phase 0.5: Git identity & GPG validation
├── commit.md        # Phases 2.0-7.0: Full --commit workflow
├── merge.md         # Full --merge workflow (CI, reviews, auto-fix)
├── watch.md         # Full --watch workflow (monitor & fix loop)
└── guardrails.md    # Safety rules, forbidden actions, timeouts
```

---

## Arguments

| Pattern | Action | Module |
|---------|--------|--------|
| `--commit` | Branch, commit, push, PR/MR | `git/identity.md` → `git/commit.md` |
| `--watch` | Monitor & fix until green | `git/watch.md` |
| `--merge` | Merge with CI validation | `git/merge.md` |
| `--finish` | Finish branch (4 options) | Inline below |
| `--help` | Display help | Inline below |

### Options --commit

| Option | Action |
|--------|--------|
| `--branch <name>` | Force the branch name |
| `--no-pr` | Skip PR/MR creation |
| `--amend` | Amend the last commit |
| `--skip-identity` | Skip git identity verification |

### Options --merge

| Option | Action |
|--------|--------|
| `--pr <number>` | Merge a specific PR (GitHub) |
| `--mr <number>` | Merge a specific MR (GitLab) |
| `--strategy <type>` | Method: merge/squash/rebase (default: squash) |
| `--dry-run` | Verify without merging |
| `--skip-review` | Skip Phase 3.5 review comments triage |

---

## --help

```
═══════════════════════════════════════════════════════════════
  /git - Workflow Git Automation (RLM)
═══════════════════════════════════════════════════════════════

Usage: /git <action> [options]

Actions:
  --commit          Full workflow (branch, commit, push, PR/MR)
  --watch           Monitor & fix until all prerequisites green
  --merge           Merge with CI validation and auto-fix
  --finish          Finish branch (merge/PR/keep/discard)

RLM Patterns:
  0.5. Identity    - Verify/configure git user via .env
  2. Peek          - Analyze git state
  3. Decompose     - Categorize files
  4. Quality Gate  - Simultaneous checks (lint+test+secret)
  6. Context       - /warmup --update (branch diff, unconditional)
  7. Synthesize    - Consolidated report

Options --commit:
  --branch <name>   Force the branch name
  --no-pr           Skip PR/MR creation
  --amend           Amend the last commit
  --skip-identity   Skip identity verification

Options --watch:
  (none)              Auto-detects PR/MR, 60s refresh, Ctrl+C to stop

Options --merge:
  --pr <number>     Merge a specific PR (GitHub)
  --mr <number>     Merge a specific MR (GitLab)
  --strategy <type> Method: merge/squash/rebase (default: squash)
  --dry-run         Verify without merging
  --skip-review     Skip review comments triage (Phase 3.5)

Identity (.env):
  - GIT_USER and GIT_EMAIL stored in /workspace/.env
  - Automatically synchronized with git config
  - Prompted to user if missing

Examples:
  /git --commit                 Automatic commit + PR
  /git --commit --no-pr         Commit without creating PR
  /git --commit --skip-identity Skip identity verification
  /git --watch                  Watch current PR until green (Ctrl+C to stop)
  /git --merge                  Merge current PR/MR
  /git --merge --pr 42          Merge PR #42

═══════════════════════════════════════════════════════════════
```

---

## MCP vs CLI Priority

**IMPORTANT**: Always prefer MCP tools when available.

**Platform auto-detected:** `git remote get-url origin` → github.com | gitlab.*

### GitHub (PRs)

| Action | Priority 1 (MCP) | Fallback (CLI) |
|--------|------------------|----------------|
| Create branch | `mcp__github__create_branch` | `git checkout -b` |
| Create PR | `mcp__github__create_pull_request` | `gh pr create` |
| List PRs | `mcp__github__list_pull_requests` | `gh pr list` |
| View PR | `mcp__github__pull_request_read` (method: get) | `gh pr view` |
| CI Status | `mcp__github__pull_request_read` (method: get_status) | _No fallback (MCP-only)_ |
| Merge PR | `mcp__github__merge_pull_request` | `gh pr merge` |

### GitLab (MRs)

| Action | Priority 1 (MCP) | Fallback (CLI) |
|--------|------------------|----------------|
| Create branch | `git checkout -b` + push | `git checkout -b` |
| Create MR | `mcp__gitlab__create_merge_request` | `glab mr create` |
| List MRs | `mcp__gitlab__list_merge_requests` | `glab mr list` |
| View MR | `mcp__gitlab__get_merge_request` | `glab mr view` |
| CI Status | `mcp__gitlab__list_pipelines` | `glab ci status` |
| Merge MR | `mcp__gitlab__merge_merge_request` | `glab mr merge` |

---

## Action: --commit

**Read modules in order:**

1. **`Read ~/.claude/commands/git/identity.md`** — Phase 0.5: Validate git identity & GPG
   - Skip if `--skip-identity` flag passed
2. **`Read ~/.claude/commands/git/commit.md`** — Phases 2.0-7.0: Full commit workflow
   - Peek → Decompose → Quality Gate → Secret Scan → Context Update → Execute

**Quick reference (see commit.md for full details):**

| Phase | Action | Key Rule |
|-------|--------|----------|
| 0.5 | Identity | GIT_USER/GIT_EMAIL from .env → git config |
| 2.0 | Peek | git status, branch check (must NOT be main) |
| 3.0 | Decompose | Categorize files (feat/fix/docs/config/test) |
| 4.0 | Quality Gate | Multi-language pre-commit (lint+build+test) |
| 5.0 | Secret Scan | Block real secrets, allow test passwords in .example |
| 6.0 | Context | `/warmup --update` on modified CLAUDE.md files |
| 7.0 | Execute | Branch → stage → commit → push → PR/MR |

---

## Action: --merge

**Read modules:**

1. **`Read ~/.claude/commands/git/merge.md`** — Full merge workflow
2. **`Read ~/.claude/commands/git/guardrails.md`** — Safety rules (referenced throughout)

**Quick reference (see merge.md for full details):**

| Phase | Action | Key Rule |
|-------|--------|----------|
| 1.0 | Peek | Pin commit SHA, verify PR/MR exists |
| 2.0 | Status Parsing | Job-level (not overall), MCP-ONLY |
| 3.0 | CI Monitoring | Exponential backoff, 10min hard timeout |
| 3.5 | Review Triage | CodeRabbit + Qodo + Codacy + Human |
| 4.0 | Error Log | Extract actionable info on failure |
| 5.0 | Auto-fix Loop | 3 attempts max, error categories |
| 5.5 | PR Regen | Regenerate title/body from final state |
| 6.0 | Merge | Squash merge + branch cleanup |

---

## Action: --watch

**Read module:**

1. **`Read ~/.claude/commands/git/watch.md`** — Full watch workflow

**Quick reference (see watch.md for full details):**

| Phase | Action | Key Rule |
|-------|--------|----------|
| 1.0 | Resolve | Auto-detect PR/MR from branch |
| 2.0 | Collect | Pipeline + Reviews + Prerequisites (parallel) |
| 3.0 | Dashboard | ASCII status display, 60s refresh |
| 4.0 | Fix Loop | Circuit breaker (stall detection >10min) |
| 5.0 | Exit | All green → ready for /git --merge |

---

## Action: --finish

```yaml
action_finish:
  trigger: "--finish"
  workflow:
    1_run_tests:
      action: "Run test suite, block if tests fail"

    2_determine_base:
      command: "git merge-base HEAD origin/main"

    3_present_options:
      tool: AskUserQuestion
      options:
        - label: "Merge locally"
          description: "Merge into main, push, delete branch"
        - label: "Push + PR"
          description: "Push and create PR for review"
        - label: "Keep as-is"
          description: "Keep the branch, no merge"
        - label: "Discard"
          description: "Delete the branch and its changes"

    4_if_discard:
      safety: "Typed confirmation: user must type 'discard' explicitly"

    5_cleanup: "Delete worktree/branch according to choice"
```

---

## Conventional Commits

| Type | Usage |
|------|-------|
| `feat` | New feature |
| `fix` | Bug fix |
| `refactor` | Refactoring |
| `docs` | Documentation |
| `test` | Tests |
| `chore` | Maintenance |
| `ci` | CI/CD |

---

## Guardrails

**Read `~/.claude/commands/git/guardrails.md` for the complete safety rules.**

**Critical rules (always enforced):**

| Action | Status |
|--------|--------|
| Push to main/master | FORBIDDEN |
| AI mentions in commits | FORBIDDEN |
| CLI for CI status | FORBIDDEN (MCP-ONLY) |
| Auto-fix security vulns | FORBIDDEN |
| Merge without CI pass | FORBIDDEN |
| Skip identity without flag | FORBIDDEN |
| Wait > 10min for CI (--merge) | FORBIDDEN |
| Auto-merge from --watch | FORBIDDEN |
| Auto-resolve human reviews | FORBIDDEN |
