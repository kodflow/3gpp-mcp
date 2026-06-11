package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

func vec1024(seed float32) []float32 {
	v := make([]float32, 1024)
	v[0] = seed
	return v
}

// buildDB writes a minimal DB at path with the given clauses; vectorise maps a
// chunk_id to a seed when that clause must carry an embedding.
func buildDB(t *testing.T, path string, clauses []model.Clause, vectorise map[uint64]float32, embeddingModel string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	_ = st.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "23.501", Release: "Rel-18", Version: "18.0.0"})
	if err := st.InsertClauses(clauses); err != nil {
		t.Fatal(err)
	}
	for id, seed := range vectorise {
		if err := st.SetEmbedding(ctx, id, vec1024(seed)); err != nil {
			t.Fatal(err)
		}
	}
	if embeddingModel != "" {
		if err := st.SetMeta("embedding_model", embeddingModel); err != nil {
			t.Fatal(err)
		}
	}
}

// TestOverlayIdentityMatch proves the overlay keys on the clause's natural identity
// (spec_id, release, clause_path, text), NOT chunk_id: a shard whose chunk_ids were
// renumbered still lands its vectors on identical clauses, and a chunk_id collision
// with DIFFERENT text must not transfer (the stale-sub-base republish window).
func TestOverlayIdentityMatch(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := filepath.Join(dir, "base.duckdb")
	shard := filepath.Join(dir, "shard.duckdb")

	buildDB(t, base, []model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "1", Heading: "h", Text: "same text"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "2", Heading: "h", Text: "NEW text"},
	}, nil, "")
	// Shard: chunk 99 matches base chunk 1 by identity (different chunk_id);
	// chunk 2 collides with base chunk 2 by chunk_id but its text is stale.
	buildDB(t, shard, []model.Clause{
		{ChunkID: 99, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "1", Heading: "h", Text: "same text"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "2", Heading: "h", Text: "OLD text"},
	}, map[uint64]float32{99: 1, 2: 2}, "model-x")

	if err := run(ctx, base, []string{shard}, ""); err != nil {
		t.Fatal(err)
	}

	st, err := store.OpenReadOnly(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var got1, got2 int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM clauses WHERE chunk_id = 1 AND embedding IS NOT NULL`).Scan(&got1); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM clauses WHERE chunk_id = 2 AND embedding IS NOT NULL`).Scan(&got2); err != nil {
		t.Fatal(err)
	}
	if got1 != 1 {
		t.Fatalf("identity match: base clause 1 should have received the vector (got %d)", got1)
	}
	if got2 != 0 {
		t.Fatalf("stale shard text must NOT transfer on a chunk_id collision (got %d)", got2)
	}
	if m := st.GetMeta(ctx, "embedding_model"); m != "model-x" {
		t.Fatalf("embedding_model not stamped: got %q want %q", m, "model-x")
	}
}

// TestOverlayRefusesMixedModels: two shards with different embedding_model must
// abort the fuse — one DB must never mix models/precisions (EmbedIdentity invariant).
func TestOverlayRefusesMixedModels(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := filepath.Join(dir, "base.duckdb")
	a := filepath.Join(dir, "a.duckdb")
	b := filepath.Join(dir, "b.duckdb")
	cl := []model.Clause{{ChunkID: 1, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "1", Heading: "h", Text: "t"}}
	buildDB(t, base, cl, nil, "")
	buildDB(t, a, cl, map[uint64]float32{1: 1}, "model-x")
	buildDB(t, b, cl, map[uint64]float32{1: 2}, "model-y")

	if err := run(ctx, base, []string{a, b}, ""); err == nil {
		t.Fatal("mixed embedding_model across shards must be refused")
	}
}
