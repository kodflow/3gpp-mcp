package mcp

import (
	"context"
	"encoding/json"

	"github.com/kodflow/3gpp-mcp/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
)

// help answers the question server_info cannot: not "what can this server do"
// but "what is actually IN it, and how do I drive it". A client that has just
// attached to the image knows neither how many specs it holds, nor which of the
// two halves carries what, nor which tool answers which question — and the
// corpus is the whole product. Capabilities live in server_info; this is the
// inventory plus the map.
//
// Every number here is COUNTED at call time, never baked in: a recap that
// restates what the build intended, rather than what the served DB holds, is the
// silent kind of wrong this project keeps finding (a corpus can lose most of its
// clauses and every fingerprint-based gate stay green).
func (h *handlers) help(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	corpus := map[string]any{"3gpp": inventoryOf(ctx, h.st)}
	if h.etsi != nil {
		corpus["etsi"] = inventoryOf(ctx, h.etsi)
	} else {
		corpus["etsi"] = map[string]any{"attached": false}
	}
	out := map[string]any{
		"what_this_is": "3GPP + ETSI specification corpus served over MCP: full clause text, " +
			"paragraph-level lineage across releases and versions, and four retrieval arms " +
			"(BM25, dense HNSW, learned-lexical sparse, cross-encoder rerank).",
		"corpus":        corpus,
		"tools":         helpTools,
		"configuration": helpConfig,
		"notes": []string{
			"The two halves are FEDERATED, never merged: a spec_id beginning \"ETSI \" is " +
				"routed to the ETSI store, everything else to the 3GPP one. Both are searched " +
				"for a federated query.",
			"server_info reports which arms are live right now and, when one is off, why — " +
				"call it before concluding an arm is missing.",
			"Lineage axis differs per half: a 3GPP spec evolves along RELEASE, an ETSI " +
				"deliverable along VERSION. trace_clause names the axis it used in its answer.",
		},
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return mcp.NewToolResultText(string(b)), nil
}

// inventoryOf counts one half at call time. A count that fails is reported as an
// error string rather than a zero: "0 clauses" and "I could not ask" must never
// look alike to a caller deciding whether the corpus is usable.
func inventoryOf(ctx context.Context, st store.Reader) map[string]any {
	inv := map[string]any{
		"attached":          true,
		"embedding_model":   st.GetMeta(ctx, "embedding_model"),
		"sparse_model":      st.GetMeta(ctx, "sparse_model"),
		"content_addressed": st.ContentAddressed(),
		"fts":               st.FTSAvailable(),
		"hnsw":              st.VSSAvailable(),
		"sparse":            st.SparseAvailable(),
	}
	count := func(key, q string) {
		var n int64
		if err := st.QueryRowContext(ctx, q).Scan(&n); err != nil {
			inv[key] = "unavailable: " + err.Error()
			return
		}
		inv[key] = n
	}
	count("specs", `SELECT count(DISTINCT spec_id) FROM clauses`)
	count("clauses", `SELECT count(*) FROM clauses`)
	count("vectors", `SELECT count(*) FROM clause_vectors`)
	// The lineage axis is the column this half actually evolves along, so the
	// recap must not hardcode "releases" for a corpus that moves by version.
	count("axis_values", `SELECT count(DISTINCT release) FROM specs`)
	return inv
}
