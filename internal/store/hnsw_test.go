package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// seedEmbedded builds a small embedded corpus: 3 clauses with distinct one-hot
// vectors so k-NN self-match is unambiguous.
func seedEmbedded(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-19", Version: "19.6.0"})
	clauses := []model.Clause{
		{ChunkID: 1, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.1", Heading: "A", Text: "alpha"},
		{ChunkID: 2, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.2", Heading: "B", Text: "beta"},
		{ChunkID: 3, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.3", Heading: "C", Text: "gamma"},
	}
	if err := st.InsertClauses(clauses); err != nil {
		t.Fatal(err)
	}
	for i := uint64(1); i <= 3; i++ {
		v := make([]float32, 1024)
		v[i*10] = 1 // distinct direction per clause
		if err := st.SetEmbedding(ctx, i, v); err != nil {
			t.Fatal(err)
		}
	}
}

func oneHot(pos int) []float32 {
	v := make([]float32, 1024)
	v[pos] = 1
	return v
}

func TestHNSWBuildFreezeAndReadOnly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hnsw.duckdb")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seedEmbedded(t, st)

	if err := st.BuildAndFreezeHNSW(ctx, "bge-m3"); err != nil {
		t.Fatalf("build-then-freeze: %v", err)
	}
	if st.GetMeta(ctx, "hnsw_state") != "frozen" {
		t.Errorf("hnsw_state = %q, want frozen", st.GetMeta(ctx, "hnsw_state"))
	}
	if st.GetMeta(ctx, "embedding_count") != "3" {
		t.Errorf("embedding_count = %q, want 3", st.GetMeta(ctx, "embedding_count"))
	}
	if !st.indexExists(ctx, "clauses_hnsw") {
		t.Error("index missing after freeze")
	}
	// Self-match: clause 2's own vector ranks clause 2 top-1.
	hits, err := st.SearchVectors(ctx, oneHot(20), SpecFilter{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Clause.ClausePath != "6.2" {
		t.Errorf("self-match top-1 = %+v, want clause 6.2", hits)
	}
	_ = st.Close()

	// Reopen read-only: the serve posture. VSS must load the frozen index.
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	if err := ro.LoadVSS(ctx); err != nil {
		t.Fatalf("read-only LoadVSS: %v", err)
	}
	if !ro.VSSAvailable() {
		t.Error("VSSAvailable false after read-only load of a frozen index")
	}
	hits, err = ro.SearchVectors(ctx, oneHot(30), SpecFilter{}, 1)
	if err != nil || len(hits) != 1 || hits[0].Clause.ClausePath != "6.3" {
		t.Errorf("read-only k-NN = %+v err=%v, want clause 6.3", hits, err)
	}
}

func TestHNSWRebuildDeterminism(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "rebuild.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	seedEmbedded(t, st)
	if err := st.BuildAndFreezeHNSW(ctx, "bge-m3"); err != nil {
		t.Fatal(err)
	}

	before, _ := st.SearchVectors(ctx, oneHot(20), SpecFilter{}, 3)
	if err := st.RebuildHNSW(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	after, _ := st.SearchVectors(ctx, oneHot(20), SpecFilter{}, 3)

	if len(before) != len(after) {
		t.Fatalf("rebuild changed result count: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if before[i].Clause.ClausePath != after[i].Clause.ClausePath {
			t.Errorf("rebuild reordered top-k at %d: %s != %s", i,
				before[i].Clause.ClausePath, after[i].Clause.ClausePath)
		}
	}
}

func TestHNSWCorruptionDegrades(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "corrupt.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	seedEmbedded(t, st)
	if err := st.BuildAndFreezeHNSW(ctx, "bge-m3"); err != nil {
		t.Fatal(err)
	}

	// Simulate count drift (column and index out of phase).
	_ = st.SetMeta("embedding_count", "999")
	if err := st.LoadVSS(ctx); err == nil {
		t.Error("LoadVSS should fail on embedding-count drift")
	}
	if st.VSSAvailable() {
		t.Error("VSSAvailable must be false when corruption is suspected")
	}
	// Exact scan still works (correctness preserved, just no index).
	hits, err := st.SearchVectors(ctx, oneHot(10), SpecFilter{}, 1)
	if err != nil || len(hits) != 1 || hits[0].Clause.ClausePath != "6.1" {
		t.Errorf("exact-scan fallback = %+v err=%v, want clause 6.1", hits, err)
	}
}
