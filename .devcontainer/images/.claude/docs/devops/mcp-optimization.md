# MCP Context Optimization

## Problem: Context Window Bloat

Each MCP tool definition consumes **400-800 tokens**. With multiple servers exposing
dozens of tools, context consumption can reach **50,000+ tokens** before any work begins.

**Impact:**
- 25-30% of 200K context window consumed by tool definitions
- Degraded model performance with cluttered context
- Higher costs per session
- Tool selection errors increase

## Solution: Tool Search (Dynamic Discovery)

Claude Code implements **Tool Search** automatically when MCP tools exceed 10% of context.

### How It Works

```
Traditional:  Load ALL tools → 50K tokens consumed
Tool Search:  Load 2 critical → Search when needed → 5K tokens
```

**Threshold:** 20K tokens (10% of 200K) triggers dynamic discovery.

## Critical Tools (Always Loaded)

For this project, keep these tools immediately available:

| Server | Tool | Reason |
|--------|------|--------|
| **context7** | `resolve-library-id`, `query-docs` | Documentation lookup |
| **github** | `pull_request_read`, `add_issue_comment` | PR/issue workflow |

All other tools are discovered via `MCPSearch` when needed.

## Best Practices

### 1. Use MCPSearch Before MCP Tools

```yaml
# CORRECT
1. MCPSearch(query="select:mcp__github__pull_request_read")
2. mcp__github__pull_request_read(...)

# INCORRECT (may fail if not loaded)
1. mcp__github__pull_request_read(...)  # Tool not in context
```

### 2. Search by Capability, Not Tool Name

```yaml
# Good - semantic search
MCPSearch(query="create pull request github")

# Less good - requires knowing exact tool name
MCPSearch(query="select:mcp__github__create_pull_request")
```

### 3. Batch Related Tool Searches

```yaml
# Efficient - one search for related tools
MCPSearch(query="github pull request review")
# Returns: pull_request_read, list_pull_requests, create_pull_request

# Inefficient - multiple searches
MCPSearch(query="get pull request")
MCPSearch(query="list pull requests")
MCPSearch(query="create pull request")
```

## Server-Specific Guidelines

### context7 (Documentation)

**Primary tools:**
- `resolve-library-id` - Find library ID for docs
- `query-docs` - Query documentation

**Recommendation:** Both tools essential, keep loaded.

### github (Repository Operations)

**High-frequency tools:**
- `pull_request_read` - PR details (method: get, get_files, get_reviews, get_comments)
- `list_pull_requests` - List PRs
- `create_pull_request` - Create PR
- `add_issue_comment` - Comment on issues

**Low-frequency tools (defer):**
- `fork_repository`
- `create_repository`
- `delete_file`

### codacy (Code Quality)

**High-frequency tools:**
- `codacy_cli_analyze` - Local analysis
- `codacy_list_repository_issues` - List issues

**Low-frequency tools (defer):**
- `codacy_setup_repository`
- `codacy_list_organizations`

### playwright (Browser Automation)

**High-frequency tools:**
- `browser_navigate`
- `browser_snapshot`
- `browser_click`

**Low-frequency tools (defer):**
- `browser_pdf_save`
- `browser_start_tracing`

### gitlab (GitLab Operations)

**High-frequency tools:**
- `get_merge_request`
- `list_merge_requests`
- `create_merge_request`

**Low-frequency tools (defer):**
- `list_pipelines`
- `create_merge_request_note`

## Token Budget Guidelines

| Component | Budget | Notes |
|-----------|--------|-------|
| System prompt | 3,000 | Fixed overhead |
| Critical MCP tools | 2,000 | context7 + github |
| Discovered tools | 3,000 | Per task, dynamic |
| Skills/Commands | 2,000 | Loaded on demand |
| Conversation | 190,000 | Available for work |

**Target:** Keep initial overhead under **5%** of context (10K tokens).

## Monitoring

Track context usage with `/cost`:

```
❯ /cost
  Total cost: $X.XX
  Usage by model:
    claude-sonnet: XXX input, XXX output, XXX cache read
```

**Warning signs:**
- Cache read > 50K on first prompt (too many tools loaded)
- Input tokens > 30K on simple tasks

## References

- [Advanced Tool Use](https://docs.anthropic.com/en/docs/build-with-claude/tool-use) - Anthropic docs
- [MCP Specification](https://modelcontextprotocol.io/) - Protocol docs
- [Claude Code MCP](https://docs.anthropic.com/en/docs/claude-code) - Integration guide

---

_Pattern: Performance > Token Economy_
_Related: [[../cloud/rate-limiting.md]], [[../performance/lazy-loading.md]]_
