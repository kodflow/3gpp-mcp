package search

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// buildLocalShard writes a one-clause vectorized sub-base (Local embedding + its
// own frozen HNSW) at path.
func buildLocalShard(t *testing.T, path, spec, text string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	_ = st.UpsertSpec(model.Spec{SpecID: spec, Series: spec[:2], DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: spec, Release: "Rel-18", Version: "18.0.0"})
	_ = st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: spec, Release: "Rel-18", Version: "18.0.0", ClausePath: "1", Heading: "h", Text: text},
	})
	v, err := embed.Local{}.Embed(ctx, []string{text})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetEmbedding(ctx, 1, v[0]); err != nil {
		t.Fatal(err)
	}
	if err := st.BuildAndFreezeHNSW(ctx, "hash-local"); err != nil {
		t.Fatal(err)
	}
}

// TestEngineVectorShards: with UseVectorShards set, the engine routes the vector
// arm through the Option-B scatter-gather over attached sub-bases.
func TestEngineVectorShards(t *testing.T) {
	t.Setenv("EMBEDDER", "local") // deterministic query vectors (same model as the shards)
	ctx := context.Background()
	dir := t.TempDir()
	a := filepath.Join(dir, "vec-23.duckdb")
	b := filepath.Join(dir, "vec-24.duckdb")
	buildLocalShard(t, a, "23.501", "amf registration procedure")
	buildLocalShard(t, b, "24.501", "smf pdu session establishment")

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	aliases, err := st.AttachShards(ctx, []string{a, b})
	if err != nil {
		t.Fatal(err)
	}

	eng := New(st)
	eng.UseVectorShards(aliases)

	// Semantic query overlapping shard A's clause → 23.501 ranks first; results
	// come from the sub-bases (the main store has no clauses).
	hits, err := eng.Search(ctx, Request{Text: "amf registration", Mode: "semantic", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("sharded vector search returned nothing")
	}
	if hits[0].Clause.SpecID != "23.501" {
		t.Fatalf("want 23.501 first (overlaps shard A), got %s", hits[0].Clause.SpecID)
	}
}
