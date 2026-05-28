package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestShardsCoherent: attached sub-bases match the client model → coherent; a
// different client model → flagged (would yield silently-wrong cosine scores).
func TestShardsCoherent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a := filepath.Join(dir, "a.duckdb")
	b := filepath.Join(dir, "b.duckdb")
	buildVecShard(t, a, "23.501", 10) // built with embedding_model "hash-local"
	buildVecShard(t, b, "24.501", 20)

	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	aliases, err := st.AttachShards(ctx, []string{a, b})
	if err != nil {
		t.Fatal(err)
	}

	if ok, why := st.ShardsCoherent(ctx, aliases, "hash-local"); !ok {
		t.Fatalf("matching model should be coherent, got %q", why)
	}
	if ok, why := st.ShardsCoherent(ctx, aliases, "bge-m3"); ok || why == "" {
		t.Fatalf("client model mismatch must be flagged, got ok=%v why=%q", ok, why)
	}
}
