package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestOverlayCarriesSparseByIdentity proves the sparse postings ride the overlay,
// re-keyed from the shard's chunk_id to the base's by natural identity (not chunk_id):
// the shard numbers the clause #7, the base #1, yet the base ends up with the
// clause's sparse terms under chunk_id 1.
func TestOverlayCarriesSparseByIdentity(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := filepath.Join(dir, "base.duckdb")
	shard := filepath.Join(dir, "shard.duckdb")

	// Base: lexical clause #1, no vectors, no sparse.
	buildDB(t, base, []model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "5.1", Heading: "AMF", Text: "amf registration"},
	}, nil, "")

	// Shard: SAME clause but renumbered #7, with a dense vector AND sparse posting.
	buildDB(t, shard, []model.Clause{
		{ChunkID: 7, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "5.1", Heading: "AMF", Text: "amf registration"},
	}, map[uint64]float32{7: 0.5}, "bge-m3")
	sh, err := store.Open(shard)
	if err != nil {
		t.Fatal(err)
	}
	if err := sh.SetSparse(ctx, 7, model.SparseVec{100: 0.9, 300: 0.2}); err != nil {
		t.Fatal(err)
	}
	_ = sh.Close()

	if err := run(ctx, base, []string{shard}, "", "bge-m3"); err != nil {
		t.Fatalf("overlay: %v", err)
	}

	// The base must now answer a sparse query on term 100 with chunk_id 1.
	bst, err := store.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bst.Close() }()
	if err := bst.LoadSparse(ctx); err != nil || !bst.SparseAvailable() {
		t.Fatalf("base sparse not available after overlay (err=%v)", err)
	}
	hits, err := bst.SearchSparse(ctx, model.SparseVec{100: 1.0}, store.SpecFilter{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Clause.ChunkID != 1 {
		t.Fatalf("sparse carry: got %v, want base chunk_id 1", hits)
	}
}
