---
name: super-claude-auto-mode-fallback
category: learned
extracted: 2026-04-26T10:45:00Z
confidence: 0.95
trigger: "When updating super-claude() function or launch config for Claude Code"
source: session
---
# super-claude: Auto Mode with Bypass Fallback

## Problem

Claude Code `--dangerously-skip-permissions` (and `defaultMode: "bypassPermissions"` in settings.json)
is BROKEN for `.claude/` and `.git/` paths since **v2.1.113** (April 17, 2026). These paths are
hardcoded as always-protected, ignoring both `permissions.allow` rules and `bypassPermissions` mode.
The user is prompted repeatedly for routine operations (e.g., writing to `.claude/contexts/`,
`.claude/plans/`, `.git/index.lock`).

Root cause: Anthropic tightened sensitive-file protection in v2.1.113 and intentionally chose NOT
to fix bypass — pivoting to Auto Mode instead.

Confirmed by: GitHub issues `anthropics/claude-code#43953`, `#37253`, `#42366`, `#36168`.

## Solution

Replace the hardcoded `--dangerously-skip-permissions` in `super-claude()` with a version-aware
flag: prefer `--permission-mode auto` when Claude Code supports `--permission-mode` (v2.1.113+),
fallback to `--dangerously-skip-permissions` for older containers.

Detection at shell init (once per session, not per invocation):

```bash
if claude --help 2>&1 | grep -q -- '--permission-mode'; then
    _CLAUDE_PERM_FLAG="--permission-mode auto"
else
    _CLAUDE_PERM_FLAG="--dangerously-skip-permissions"
fi
```

## Example

```bash
# BAD — hardcoded bypass (broken on v2.1.113+ for .claude/ .git/)
super-claude() {
    claude --dangerously-skip-permissions "$@"
}

# GOOD — auto mode with fallback
if claude --help 2>&1 | grep -q -- '--permission-mode'; then
    _CLAUDE_PERM_FLAG="--permission-mode auto"
else
    _CLAUDE_PERM_FLAG="--dangerously-skip-permissions"
fi

super-claude() {
    claude ${_CLAUDE_PERM_FLAG} "$@"
}
```

## When to Use

- Any time `super-claude()` is defined or regenerated (postCreate.sh, devcontainer-env.sh)
- When a user reports "bypass mode still prompts" on `.claude/` or `.git/` paths
- When building a new container image that should work without user interaction

## Why Auto Mode is Better Than Bypass

| | bypass (broken v2.1.113+) | --permission-mode auto |
|---|---|---|
| `.claude/`, `.git/` prompts | YES (regression) | No |
| Truly destructive ops | Sometimes no prompt | Prompted (model judges) |
| Mechanism | Hardcoded path list | ML classifier per-action |
| Future-proof | No (more paths added) | Yes |

## Files to Update

- `~/.devcontainer-env.sh` — live shell function (current container)
- `.devcontainer/images/hooks/lifecycle/postCreate.sh` — template (new containers)

## Evidence

Discovered 2026-04-26 after user reported 11 consecutive permission prompts during `/search`
parallel agent execution. Research confirmed via 10 parallel agents + GitHub issue analysis.
