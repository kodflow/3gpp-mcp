<!-- updated: 2026-04-10T12:00:00Z -->
# Claude Feature

## Purpose

Standalone Claude Code installation for projects without the full DevContainer.

## Installation

```bash
curl -sL https://raw.githubusercontent.com/kodflow/devcontainer-template/main/.devcontainer/features/claude/install.sh | bash
```

## What Gets Installed

| Component | Description |
|-----------|-------------|
| Claude CLI | AI coding assistant |
| Commands | /git, /search, /prompt |
| Hooks | format, lint, security, test |
| status-line | Git branch/status display |

## Native Claude 2.x Features

This feature leverages Claude's native capabilities:

- **EnterPlanMode** - Built-in planning mode
- **TaskCreate/TaskUpdate/TaskList** - Task tracking with progress visualization
- **Task agents** - Parallel execution

## Configuration

Settings are stored in `~/.claude/settings.json`.
MCP servers configured in `mcp.json` (generated at runtime from `mcp.json.tpl` + feature fragments in `/etc/mcp/features/`).

## See Also

- Root [CLAUDE.md](/CLAUDE.md) for project conventions
- [.devcontainer/CLAUDE.md](../) for DevContainer config
