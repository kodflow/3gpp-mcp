package search

import (
	"context"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestSearchModeDegrade checks the mode gating and the degrade-don't-block rule:
// with no embedder, "semantic" must still return results (falling back to
// lexical), never an empty list.
func TestSearchModeDegrade(t *testing.T) {
	t.Setenv("EMBEDDER", "off") // no vectors → exercises the degrade path
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.1", Heading: "AMF registration", Text: "the amf registration procedure over LI_X2"},
		{ChunkID: 2, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.2", Heading: "SMF session", Text: "pdu session establishment"},
	}); err != nil {
		t.Fatal(err)
	}
	eng := New(st)

	for _, mode := range []string{"lexical", "semantic", "hybrid", ""} {
		hits, err := eng.Search(ctx, Request{Text: "registration", Mode: mode, TopK: 5})
		if err != nil {
			t.Fatalf("mode=%q: %v", mode, err)
		}
		if len(hits) == 0 {
			t.Fatalf("mode=%q returned no hits (degrade-don't-block violated)", mode)
		}
		if hits[0].Clause.ChunkID != 1 {
			t.Fatalf("mode=%q: want clause 1 top (matches 'registration'), got %d", mode, hits[0].Clause.ChunkID)
		}
	}
}
