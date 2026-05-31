<!-- updated: 2026-05-30T08:38:41Z -->
# internal/mcp — MCP Tool Surface

## Purpose

Wires the MCP (Model Context Protocol) tool surface over stdio (SSE optional),
using `github.com/mark3labs/mcp-go`. Eight tools, no more (CLAUDE.md §5). Every
response carries a `citations` block; tools **refuse to answer when they cannot
cite** (CLAUDE.md §1: "Pas d'hallucination tolérée").

## Structure

```text
mcp/
├── server.go      # server wiring, tool registration, subject tool injection
├── resources.go   # MCP resources (read-only catalog/spec exposure)
├── pagination.go  # cursor-based pagination for list responses
└── doc.go         # 8-tool contract
```

## The 8 tools (CLAUDE.md §5)

`search_spec` · `get_spec` · `get_changelog` · `list_releases` · `resolve_term`
· `trace_evolution` (V2) · `find_cross_references` · `list_specs`

Subjects contribute extra tools via `internal/subject` (e.g. LI's `li_events`);
those are registered through `registry`, not hardcoded here.

## Conventions

- **No résumés server-side** — return documents/fragments; Claude synthesises
  (CLAUDE.md §13). The server is a retrieval engine, not an assistant.
- Do not add a 9th core tool without an `arch-change` justification (§5 is figé).
- All list-returning tools paginate via `pagination.go`.
- `li_events_test` / `resources_test` / `server_test` pin the surface contract.
