package mcp

// helpTools is the map from question to tool. It is written for a caller who has
// twelve tools and no idea which one answers "what changed between Rel-17 and
// Rel-18" — the descriptions on each tool say what it does, not when to reach
// for it.
var helpTools = []map[string]string{
	{"tool": "search_spec", "use_when": "free-text question over clause bodies; the main entry point",
		"note": "mode=lexical|semantic|hybrid (default hybrid when the arms are live)"},
	{"tool": "get_spec", "use_when": "you already know the spec and clause and want the text",
		"note": "full=true for the whole body, otherwise a bounded snippet plus a resource URI"},
	{"tool": "search_api", "use_when": "you are after a 5GC OpenAPI operation or schema, not prose",
		"note": "SHA-pinned citations from the canonical 3GPP Forge YAML"},
	{"tool": "trace_clause", "use_when": "how a clause's TEXT changed, paragraph by paragraph",
		"note": "from_release+to_release gives the +/- between two points; reports the axis it used"},
	{"tool": "trace_evolution", "use_when": "how a 4G element maps onto its 5GC network function(s)"},
	{"tool": "get_changelog", "use_when": "the change records between two releases of one spec"},
	{"tool": "list_releases", "use_when": "which releases/versions of a spec exist in the corpus"},
	{"tool": "list_specs", "use_when": "browse the catalogue by release, series or working group"},
	{"tool": "find_cross_references", "use_when": "which specs a spec or clause points at"},
	{"tool": "resolve_term", "use_when": "expand an acronym or find where a term is defined"},
	{"tool": "li_events", "use_when": "lawful-interception event definitions (TS 33.128)"},
	{"tool": "server_info", "use_when": "which retrieval arms are live, and WHY one is off"},
	{"tool": "help", "use_when": "this recap: what the corpus holds and how to drive it"},
}

// helpConfig lists only the knobs that change what a CLIENT gets back. Build-time
// and pipeline variables are deliberately absent: a user configuring the served
// image cannot act on them, and listing them invites cargo-culting.
var helpConfig = []map[string]string{
	{"env": "RT_DB", "effect": "path to the 3GPP DuckDB served (default data/3gpp.duckdb)"},
	{"env": "RT_DB_FULL", "effect": "path to the ETSI DuckDB attached alongside; unset = 3GPP only"},
	{"env": "EMBEDDER", "effect": "off disables the query embedder: semantic and sparse go dark, BM25 remains"},
	{"env": "RERANKER", "effect": "off disables the cross-encoder rerank pass"},
	{"env": "RERANK_WINDOW", "effect": "how many candidates the cross-encoder rescores (default 12)"},
	{"env": "RERANK_ALL", "effect": "true reranks every arm's candidates, not just the hybrid head"},
	{"env": "SEARCH_BUDGET", "effect": "wall-clock budget per search before partial results are returned (default 20s)"},
	{"env": "EMBED_QUERY_CACHE", "effect": "query-embedding cache entries (default 512)"},
	{"env": "MCP3GPP_ALLOW_LEXICAL_FALLBACK", "effect": "true lets the server start lexically when vectors are unusable; " +
		"default refuses to start, so a silently degraded server is never served"},
	{"env": "ORT_EP", "effect": "ONNX Runtime execution provider (cpu, cuda)"},
}
