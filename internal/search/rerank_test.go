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

// THE FUSED ROUTE: a caller that merges several searches reranks the merged head
// itself, and the searches it merged must not rerank at all — including under the
// always-rerank toggle, which Search reads on its own (Qodo, #348). This is the
// engine half of what search_spec does for a federated call
// (internal/mcp/federated_rerank.go).
func TestDeferRerankLeavesTheCrossEncoderToTheCaller(t *testing.T) {
	t.Setenv("RERANKER", "lexical")
	t.Setenv("EMBEDDER", "off")
	t.Setenv("SEARCH_BUDGET", "0")
	t.Setenv("RERANK_ALL", "1")
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS"})
	_ = st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "33.128", Release: "Rel-17", Version: "17.0.0", ClausePath: "1", Heading: "Scope", Text: "registration mentioned once here"},
		{ChunkID: 2, SpecID: "33.128", Release: "Rel-17", Version: "17.0.0", ClausePath: "6.2.2.2", Heading: "AMF registration event over X2", Text: "AMF registration event delivered over LI_X2 to the MDF2"},
		{ChunkID: 3, SpecID: "33.128", Release: "Rel-17", Version: "17.0.0", ClausePath: "8", Heading: "SMF", Text: "registration of session"},
	})
	eng := New(st)
	eng.SetName("3gpp")
	if !eng.RerankEvery() {
		t.Fatal("RERANK_ALL=1 did not reach the engine — the test would prove nothing")
	}

	// Deferred: the search fuses and stops there, and says nothing about a rerank.
	ctx1, tr1 := WithTrace(ctx)
	fused, err := eng.Search(ctx1, Request{Text: "AMF registration event over X2", TopK: 3, DeferRerank: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range tr1.Reports() {
		for _, a := range r.Arms {
			if a.Arm == ArmRerank {
				t.Fatalf("a deferred search reported a rerank arm: %+v", a)
			}
		}
	}

	// The caller's own pass: it reorders, and it is reported under the name the
	// caller gives it.
	ctx2, tr2 := WithTrace(ctx)
	out := eng.RerankFused(ctx2, "AMF registration event over X2", fused, "federated")
	if len(out) == 0 || out[0].Clause.ClausePath != "6.2.2.2" {
		t.Errorf("fused rerank top-1 = %v, want clause 6.2.2.2 (max overlap)", hitPaths(out))
	}
	reps := tr2.Reports()
	if len(reps) != 1 || reps[0].Corpus != "federated" || len(reps[0].Arms) != 1 ||
		reps[0].Arms[0].Arm != ArmRerank || !reps[0].Arms[0].Ran {
		t.Fatalf("the fused pass reported %+v, want one rerank arm under \"federated\"", reps)
	}
}
