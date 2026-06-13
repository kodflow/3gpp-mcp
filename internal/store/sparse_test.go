package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestSparseSetSearchAndFilter pins the inverted-index sparse arm: dot-product
// ranking over shared term ids, idempotent re-set, and the SpecFilter.
func TestSparseSetSearchAndFilter(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "sparse.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	_ = st.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS"})
	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS"})
	clauses := []model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-18", Version: "18.6.0", ClausePath: "5.1", Heading: "AMF", Text: "amf"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-18", Version: "18.6.0", ClausePath: "5.2", Heading: "SMF", Text: "smf"},
		{ChunkID: 3, SpecID: "33.128", Release: "Rel-18", Version: "18.6.0", ClausePath: "6.1", Heading: "LI", Text: "li"},
	}
	if err := st.InsertClauses(clauses); err != nil {
		t.Fatal(err)
	}

	// Before any sparse rows: arm unavailable, search returns nothing.
	if err := st.LoadSparse(ctx); err != nil {
		t.Fatal(err)
	}
	if st.SparseAvailable() {
		t.Fatal("SparseAvailable=true on an empty clause_sparse")
	}

	// term 100 = "amf-ish", term 200 = "smf-ish", term 300 = shared/common.
	if err := st.SetSparse(ctx, 1, model.SparseVec{100: 0.9, 300: 0.1}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSparse(ctx, 2, model.SparseVec{200: 0.8, 300: 0.2}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSparse(ctx, 3, model.SparseVec{100: 0.3, 300: 0.5}); err != nil {
		t.Fatal(err)
	}
	if err := st.LoadSparse(ctx); err != nil {
		t.Fatal(err)
	}
	if !st.SparseAvailable() {
		t.Fatal("SparseAvailable=false after populating clause_sparse")
	}

	// Query strongly on term 100 → clause 1 (0.9*… ) must outrank clause 3 (0.3*…).
	hits, err := st.SearchSparse(ctx, model.SparseVec{100: 1.0, 300: 0.1}, SpecFilter{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 2 || hits[0].Clause.ChunkID != 1 {
		t.Fatalf("top hit chunk=%v (want 1); got %d hits", hitsTop(hits), len(hits))
	}
	// clause 2 shares only term 300 (weight 0.2*0.1=0.02) → present but last-ish.

	// SpecFilter restricts to 33.128 → only clause 3 can match term 100.
	f, err := st.SearchSparse(ctx, model.SparseVec{100: 1.0}, SpecFilter{SpecID: "33.128"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 1 || f[0].Clause.ChunkID != 3 {
		t.Fatalf("filtered sparse search = %v, want [3]", hitsTop(f))
	}

	// Idempotent re-set: clause 1's term 100 removed → no longer the top match.
	if err := st.SetSparse(ctx, 1, model.SparseVec{300: 0.1}); err != nil {
		t.Fatal(err)
	}
	hits2, err := st.SearchSparse(ctx, model.SparseVec{100: 1.0}, SpecFilter{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits2) != 1 || hits2[0].Clause.ChunkID != 3 {
		t.Fatalf("after re-set, term-100 search = %v, want only clause 3", hitsTop(hits2))
	}
}

func hitsTop(hits []model.SearchHit) []uint64 {
	out := make([]uint64, len(hits))
	for i, h := range hits {
		out[i] = h.Clause.ChunkID
	}
	return out
}
