package store

import (
	"context"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestBackfillDuplicateEmbeddings checks that an embedded clause's vector is copied onto
// every still-unembedded clause with byte-identical (heading, text) — the cross-release/
// cross-series dedup that avoids re-embedding verbatim-repeated clauses on the GPU.
func TestBackfillDuplicateEmbeddings(t *testing.T) {
	ctx := context.Background()
	s := newMem(t)
	// Chunks 1,2,3 share (heading,text) "amf"/"select" across three releases; chunk 4 is
	// a different text. Only chunk 1 gets a vector; backfill must fill 2 and 3, not 4.
	if err := s.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-17", Version: "17.0.0", ClausePath: "5.2", Heading: "amf", Text: "select"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "5.2", Heading: "amf", Text: "select"},
		{ChunkID: 3, SpecID: "23.501", Release: "Rel-19", Version: "19.0.0", ClausePath: "5.2", Heading: "amf", Text: "select"},
		{ChunkID: 4, SpecID: "23.502", Release: "Rel-19", Version: "19.0.0", ClausePath: "4.3", Heading: "pdu", Text: "session"},
	}); err != nil {
		t.Fatal(err)
	}

	v := make([]float32, 1024)
	v[0], v[42] = 0.5, -0.25
	if err := s.SetEmbeddingWithHash(ctx, 1, v, "hash-amf"); err != nil {
		t.Fatalf("seed embed: %v", err)
	}

	// Before: 3 NULL (chunks 2,3,4).
	if n, err := s.CountNullEmbeddings(ctx); err != nil || n != 3 {
		t.Fatalf("before backfill null=%d err=%v, want 3", n, err)
	}

	got, err := s.BackfillDuplicateEmbeddings(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if got != 2 {
		t.Fatalf("backfilled %d, want 2 (chunks 2 and 3)", got)
	}

	// After: only chunk 4 (distinct text) remains NULL.
	if n, err := s.CountNullEmbeddings(ctx); err != nil || n != 1 {
		t.Fatalf("after backfill null=%d err=%v, want 1", n, err)
	}
	// Chunk 3 carries chunk 1's vector + hash.
	var val float32
	if err := s.DB().QueryRowContext(ctx,
		`SELECT embedding[43] FROM clauses WHERE chunk_id = 3`).Scan(&val); err != nil {
		t.Fatalf("read back chunk 3: %v", err)
	}
	if val != -0.25 {
		t.Errorf("chunk 3 embedding[43] = %v, want -0.25 (copied from chunk 1)", val)
	}
	var h string
	if err := s.DB().QueryRowContext(ctx,
		`SELECT embedding_hash FROM clauses WHERE chunk_id = 2`).Scan(&h); err != nil {
		t.Fatalf("read hash chunk 2: %v", err)
	}
	if h != "hash-amf" {
		t.Errorf("chunk 2 embedding_hash = %q, want hash-amf", h)
	}

	// Idempotent: a second call backfills nothing.
	if got, err := s.BackfillDuplicateEmbeddings(ctx); err != nil || got != 0 {
		t.Fatalf("second backfill got %d err=%v, want 0", got, err)
	}
}
