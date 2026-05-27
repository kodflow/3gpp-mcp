# Token Optimization Guide

## Overview

Token usage directly impacts cost and context window management. This guide documents strategies to minimize token consumption while maintaining quality.

## 1. RTK (Rust Token Killer)

RTK transparently rewrites CLI commands to compress their output. Integrated via `rtk hook claude` PreToolUse hook (the rtk binary owns the hook protocol upstream — no shell wrapper).

| Category | Avg Savings | Examples |
|----------|-------------|---------|
| Git | 75-94% | status, diff, log |
| Tests | ~90% | cargo test, pytest, vitest |
| Build/Lint | ~80% | tsc, eslint, clippy |
| Files | ~70% | ls, cat, find |
| Containers | ~75% | docker ps, kubectl |

**Check savings:** `rtk gain` (analytics), `rtk discover` (missed opportunities).

## 2. MCP vs CLI Strategy

| Use MCP When | Use CLI When |
|--------------|-------------|
| Structured data needed (PRs, issues) | Simple one-shot commands |
| Auth is pre-configured | No MCP server available |
| Multiple related API calls | Bandwidth-sensitive operations |
| Need consistent response format | RTK can compress the output |

**Rule:** MCP-first for GitHub/GitLab/Codacy operations. CLI+RTK for everything else.

## 3. Model Routing

Delegate the cheapest sufficient model via agent specialization.

| Task Type | Model | Cost Ratio | Examples |
|-----------|-------|------------|---------|
| Architecture, planning, complex reasoning | Opus | 1x (baseline) | developer-orchestrator, developer-commentator |
| Code generation, review, analysis | Sonnet | ~0.2x | Language specialists, devops specialists |
| Routing, simple checks, OS queries | Haiku | ~0.04x | OS specialists, docs analyzers, quality checks |

**Model distribution (81 agents):** 3 Opus, 32 Sonnet, 46 Haiku.

## 4. Context Management

### Compaction Strategy

- Auto-compaction triggers at 85% context usage (`CLAUDE_CODE_AUTOCOMPACT_PCT_OVERRIDE`)
- PreCompact hook saves critical state before compaction
- Checkpoint files in `/workspace/.claude/logs/<branch>/checkpoint.json`

### Reducing Context Load

- Skills load on-demand (not all 17 at once)
- Agent definitions loaded only when spawned
- Design patterns (170+ files) accessed via targeted Read on specific category, not bulk reads
- Hook profiles (`HOOK_PROFILE=minimal`) reduce hook output in context

## 5. Subagent Architecture

Subagents protect the main context window by running in isolated forks.

**Pattern:** Orchestrator (Opus) delegates to Specialists (Sonnet/Haiku) → results merged as condensed JSON.

**Benefits:**
- Each subagent has fresh context (no accumulated history)
- Failed subagents don't pollute parent context
- Parallel execution reduces wall-clock time
- Cheaper models handle most work

## 6. Session Persistence

Instead of re-exploring the codebase each session:
- `SessionStart` hook loads cached metadata
- `/warmup` pre-loads CLAUDE.md hierarchy
- Checkpoint files enable session recovery
- `/learn` extracts reusable patterns to avoid repeated exploration
