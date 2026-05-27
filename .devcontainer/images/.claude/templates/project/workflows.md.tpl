# Development Workflows

## Quick Reference

| Task | Command |
|------|---------|
| Verify setup | `/init` |
| New feature | `/feature <desc>` |
| Bug fix | `/fix <desc>` |
| Code review | `/review` |
| Run tests | `{{TEST_COMMAND}}` |

## Feature Development

```
/init → /feature "description" → implement → /review → PR
```

1. Verify environment with `/init`
2. Create feature branch with `/feature "description"`
3. Implement changes (planning mode activated)
4. Run `/review` for code quality check
5. PR created automatically

## Bug Fixes

```
/init → /fix "description" → implement → /review → PR
```

Same flow as features, uses `fix/` branch prefix.

## Branch Conventions

| Type | Branch | Commit |
|------|--------|--------|
| Feature | `feat/<desc>` | `feat(scope): message` |
| Bug fix | `fix/<desc>` | `fix(scope): message` |

## Pre-commit Checks

{{PRE_COMMIT_CHECKS}}

## MCP Integration

Prefer MCP tools over CLI:
{{#MCP_TOOLS}}
- `{{MCP_TOOL}}` before `{{CLI_FALLBACK}}`
{{/MCP_TOOLS}}

## Search Strategy

1. **Exact strings / regex**: Grep (primary)
2. **File discovery**: Glob
3. **Read-then-understand**: Read full files; agents reason from there
