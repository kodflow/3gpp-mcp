package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestSearchVectorsKNNAndFilter checks the bare-k-NN path returns the nearest
// clause and that the SpecFilter is applied (in Go) after the index lookup.
func TestSearchVectorsKNNAndFilter(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "v.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	seedEmbedded(t, st) // chunks 1/2/3 at one-hot positions 10/20/30, Rel-19, series 33
	if err := st.BuildAndFreezeHNSW(ctx, "bge-m3"); err != nil {
		t.Fatal(err)
	}

	// Query in chunk 2's direction → chunk 2 is the nearest neighbour.
	hits, err := st.SearchVectors(ctx, oneHot(20), SpecFilter{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Clause.ChunkID != 2 {
		t.Fatalf("want top hit chunk 2, got %+v", hits)
	}

	// A filter that matches nothing must drop all neighbours (post-filter in Go).
	none, err := st.SearchVectors(ctx, oneHot(20), SpecFilter{Release: "Rel-18"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("non-matching filter should exclude all, got %d", len(none))
	}

	// A matching filter still returns.
	some, err := st.SearchVectors(ctx, oneHot(20), SpecFilter{Release: "Rel-19", Series: "33"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(some) == 0 || some[0].Clause.ChunkID != 2 {
		t.Fatalf("matching filter want chunk 2, got %+v", some)
	}
}

// TestSearchVectorsAmongBounded checks the no-HNSW fallback ranks ONLY the given
// candidate ids (a bounded exact scan, never the whole corpus).
func TestSearchVectorsAmongBounded(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "a.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	seedEmbedded(t, st) // no HNSW built → exercises the fallback path

	// Candidates {1,3} exclude chunk 2, even though oneHot(20) is nearest to it.
	hits, err := st.SearchVectorsAmong(ctx, oneHot(20), []uint64{1, 3}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 candidates ranked, got %d", len(hits))
	}
	for _, h := range hits {
		if h.Clause.ChunkID == 2 {
			t.Fatal("chunk 2 is not a candidate and must not appear")
		}
	}

	// Empty candidate set → no hits, no error.
	if h, err := st.SearchVectorsAmong(ctx, oneHot(20), nil, 5); err != nil || len(h) != 0 {
		t.Fatalf("empty candidates → want (nil,nil), got (%d,%v)", len(h), err)
	}
}

// TestSearchVectorsDocTypeFilter guards the vector path against leaking a clause
// of the wrong doc_type past an explicit SpecFilter.DocType (TS/TR), even when
// that clause is the nearest neighbour.
func TestSearchVectorsDocTypeFilter(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "dt.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS"})
	_ = st.UpsertSpec(model.Spec{SpecID: "33.926", Series: "33", DocType: "TR"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-19", Version: "19.6.0"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.926", Release: "Rel-19", Version: "19.0.0"})
	if err := st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.1", Heading: "TS", Text: "x"},
		{ChunkID: 2, SpecID: "33.926", Release: "Rel-19", Version: "19.0.0", ClausePath: "4.1", Heading: "TR", Text: "y"},
	}); err != nil {
		t.Fatal(err)
	}
	// Both clauses share the SAME direction → the TR clause is an equally-near
	// neighbour and would surface without the DocType post-filter.
	v := oneHot(20)
	for _, id := range []uint64{1, 2} {
		if err := st.SetEmbedding(ctx, id, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.BuildAndFreezeHNSW(ctx, "bge-m3"); err != nil {
		t.Fatal(err)
	}
	hits, err := st.SearchVectors(ctx, v, SpecFilter{DocType: "TS"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("expected a TS hit")
	}
	for _, h := range hits {
		if h.Clause.SpecID == "33.926" {
			t.Fatal("TR spec leaked past DocType=TS filter")
		}
	}
}
