package search

import (
	"context"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// With a reranker enabled and Rerank=true, the engine re-scores the fused window
// so the passage with the highest query-token overlap surfaces first — even when
// the lexical arm alone wouldn't rank it top.
func TestEngineRerank(t *testing.T) {
	t.Setenv("RERANKER", "lexical")
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-17", Version: "17.0.0"})
	_ = st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "33.128", Release: "Rel-17", Version: "17.0.0", ClausePath: "1", Heading: "Scope", Text: "registration mentioned once here"},
		{ChunkID: 2, SpecID: "33.128", Release: "Rel-17", Version: "17.0.0", ClausePath: "6.2.2.2", Heading: "AMF registration event over X2", Text: "AMF registration event delivered over LI_X2 to the MDF2"},
		{ChunkID: 3, SpecID: "33.128", Release: "Rel-17", Version: "17.0.0", ClausePath: "8", Heading: "SMF", Text: "registration of session"},
	})

	eng := New(st)
	if eng.rr == nil || !eng.rr.Enabled() {
		t.Fatal("expected lexical reranker enabled via env")
	}
	hits, err := eng.Search(ctx, Request{
		Text: "AMF registration event over X2", TopK: 3, Rerank: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Clause.ClausePath != "6.2.2.2" {
		t.Errorf("rerank top-1 = %+v, want clause 6.2.2.2 (max overlap)", hitPaths(hits))
	}
}

func hitPaths(hits []model.SearchHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Clause.ClausePath
	}
	return out
}
