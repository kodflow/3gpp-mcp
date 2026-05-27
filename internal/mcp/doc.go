// Package mcp wires the MCP (Model Context Protocol) tool surface.
//
// Eight tools, no more (CLAUDE.md §5):
//
//	search_spec            — hybrid retrieval with citations
//	get_spec               — fetch a spec or a single clause
//	get_changelog          — list CRs between two releases
//	list_releases          — versions + freeze dates
//	resolve_term           — glossary lookup
//	trace_evolution        — V2: NE↔NF subgraph
//	find_cross_references  — referenced specs/clauses
//	list_specs             — catalog filter (release/series/WG)
//
// Every response carries a `citations` block. Tools refuse to answer
// when they cannot cite (CLAUDE.md §1: "Pas d'hallucination tolérée").
//
// Transport is stdio first; SSE is optional once the binary is hosted.
// Implementation uses github.com/mark3labs/mcp-go.
package mcp
