package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// buildVecShard writes a one-clause vectorized sub-base (its own frozen HNSW) at
// path: chunk 1 of spec, embedded at one-hot position pos.
func buildVecShard(t *testing.T, path, spec string, pos int) {
	t.Helper()
	ctx := context.Background()
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	_ = st.UpsertSpec(model.Spec{SpecID: spec, Series: spec[:2], DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: spec, Release: "Rel-18", Version: "18.0.0"})
	if err := st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: spec, Release: "Rel-18", Version: "18.0.0", ClausePath: "1", Heading: "h", Text: "t"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetEmbedding(ctx, 1, oneHot(pos)); err != nil {
		t.Fatal(err)
	}
	if err := st.BuildAndFreezeHNSW(ctx, "hash-local"); err != nil {
		t.Fatal(err)
	}
}

// TestSearchVectorsSharded is the Option-B scatter-gather: two per-series
// sub-bases attached read-only, queried in one shot, merged by distance.
func TestSearchVectorsSharded(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a := filepath.Join(dir, "vec-23.duckdb")
	b := filepath.Join(dir, "vec-24.duckdb")
	buildVecShard(t, a, "23.501", 10) // nearest to oneHot(10)
	buildVecShard(t, b, "24.501", 20) // nearest to oneHot(20)

	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	aliases, err := st.AttachShards(ctx, []string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 2 {
		t.Fatalf("want 2 aliases, got %v", aliases)
	}

	// Query in shard B's direction → B's clause (24.501) ranks first; both shards
	// contribute candidates (2 hits total).
	hits, err := st.SearchVectorsSharded(ctx, oneHot(20), aliases, SpecFilter{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("scatter-gather should union both shards, got %d hits", len(hits))
	}
	if hits[0].Clause.SpecID != "24.501" {
		t.Fatalf("query in shard-B direction should rank 24.501 first, got %s", hits[0].Clause.SpecID)
	}

	// Query in shard A's direction → 23.501 first.
	hitsA, err := st.SearchVectorsSharded(ctx, oneHot(10), aliases, SpecFilter{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hitsA) == 0 || hitsA[0].Clause.SpecID != "23.501" {
		t.Fatalf("query in shard-A direction should rank 23.501 first, got %+v", hitsA)
	}

	// Filter to one series → only that shard's clause survives the post-filter.
	hitsF, err := st.SearchVectorsSharded(ctx, oneHot(20), aliases, SpecFilter{Series: "24"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hitsF) != 1 || hitsF[0].Clause.SpecID != "24.501" {
		t.Fatalf("series=24 filter should yield only 24.501, got %+v", hitsF)
	}
}
