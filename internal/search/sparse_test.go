package search

import (
	"context"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestSparseArmParticipatesAndToggles verifies the engine exposes the sparse arm
// when the embedder is sparse-capable AND clause_sparse is populated, that it
// fuses into results, and that the runtime toggle gates it.
func TestSparseArmParticipatesAndToggles(t *testing.T) {
	t.Setenv("EMBEDDER", "local") // Local implements SparseEmbedder (deterministic)
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	clauses := []model.Clause{
		{ChunkID: 1, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.1", Heading: "AMF registration", Text: "amf registration procedure"},
		{ChunkID: 2, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.2", Heading: "SMF session", Text: "pdu session establishment"},
	}
	if err := st.InsertClauses(clauses); err != nil {
		t.Fatal(err)
	}

	// Populate sparse postings from the SAME deterministic embedder the engine uses.
	sp := embed.Local{}
	for _, c := range clauses {
		vecs, err := sp.EmbedSparse(ctx, []string{c.Heading + "\n" + c.Text})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetSparse(ctx, c.ChunkID, vecs[0]); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.LoadSparse(ctx); err != nil {
		t.Fatal(err)
	}

	eng := New(st)
	if s := eng.State(); !s.SparseEnabled {
		t.Fatal("State.SparseEnabled=false despite local sparse embedder + populated clause_sparse")
	}

	// Sparse arm on: a query matching clause 1's terms ranks it first.
	hits, err := eng.Search(ctx, Request{Text: "amf registration", Mode: "hybrid", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Clause.ChunkID != 1 {
		t.Fatalf("hybrid+sparse top hit = %v, want clause 1", topIDs(hits))
	}

	// Toggle the sparse arm off → State reflects it, search still succeeds.
	eng.SetSparse(false)
	if eng.State().SparseOn {
		t.Fatal("SparseOn=true after SetSparse(false)")
	}
	if _, err := eng.Search(ctx, Request{Text: "amf registration", Mode: "hybrid", TopK: 5}); err != nil {
		t.Fatalf("search with sparse off: %v", err)
	}
}

func topIDs(hits []model.SearchHit) []uint64 {
	out := make([]uint64, len(hits))
	for i, h := range hits {
		out[i] = h.Clause.ChunkID
	}
	return out
}
