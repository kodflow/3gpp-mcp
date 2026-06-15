package main

import (
	"context"
	"database/sql"
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

// TestOverlayAcceptsCompactSparseShard locks the Kaggle sparse kernel's shard schema:
// the shard's `clauses` table is built by a RAW `CREATE TABLE … SELECT` (NOT the store
// schema), carrying identity + text + NULL embedding/embedding_hash columns, plus
// clause_sparse. overlay's dense UPDATE reads s.embedding, so those NULL columns MUST
// exist or the whole overlay errors with "column embedding not found" — the latent bug
// that never surfaced because the shard was never produced (missing duckdb CLI).
func TestOverlayAcceptsCompactSparseShard(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := filepath.Join(dir, "base.duckdb")
	shard := filepath.Join(dir, "shard.duckdb")

	// Base: lexical clause #1, no vectors, no sparse.
	buildDB(t, base, []model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "5.1", Heading: "AMF", Text: "amf registration"},
	}, nil, "")

	// Shard: exactly what kernel-sparse-corpus.py emits — a raw clauses table with the
	// identity columns + NULL embedding/embedding_hash, and clause_sparse. Clause is
	// renumbered #7 to also exercise the identity (not chunk_id) re-key.
	raw, err := sql.Open("duckdb", shard)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE clauses (chunk_id UBIGINT, spec_id VARCHAR, release VARCHAR, version VARCHAR,
		   clause_path VARCHAR, text VARCHAR, embedding FLOAT[1024], embedding_hash VARCHAR)`,
		`INSERT INTO clauses (chunk_id, spec_id, release, version, clause_path, text)
		   VALUES (7, '23.501', 'Rel-18', '18.0.0', '5.1', 'amf registration')`,
		`CREATE TABLE clause_sparse (chunk_id UINTEGER, term_id UINTEGER, weight FLOAT)`,
		`INSERT INTO clause_sparse VALUES (7, 100, 0.9), (7, 300, 0.2)`,
	} {
		if _, err := raw.ExecContext(ctx, q); err != nil {
			_ = raw.Close()
			t.Fatalf("build compact shard (%s): %v", q, err)
		}
	}
	_ = raw.Close()

	if err := run(ctx, base, []string{shard}, "", ""); err != nil {
		t.Fatalf("overlay compact shard: %v", err)
	}

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
		t.Fatalf("compact shard sparse carry: got %v, want base chunk_id 1", hits)
	}
}
