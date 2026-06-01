package store

import (
	"context"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestSetEmbeddingsBatch checks the batched writer is equivalent to per-row writes
// (same vectors + hashes land) and rejects ragged input.
func TestSetEmbeddingsBatch(t *testing.T) {
	ctx := context.Background()
	s := newMem(t)
	if err := s.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-19", Version: "19.0.0", ClausePath: "1", Heading: "a", Text: "x"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-19", Version: "19.0.0", ClausePath: "2", Heading: "b", Text: "y"},
		{ChunkID: 3, SpecID: "23.501", Release: "Rel-19", Version: "19.0.0", ClausePath: "3", Heading: "c", Text: "z"},
	}); err != nil {
		t.Fatal(err)
	}

	v1 := make([]float32, 1024)
	v1[0], v1[7] = 0.5, -0.5
	v2 := make([]float32, 1024)
	v2[1] = 1
	v3 := make([]float32, 1024)
	v3[1023] = 0.25

	if err := s.SetEmbeddingsBatch(ctx,
		[]uint64{1, 2, 3}, [][]float32{v1, v2, v3}, []string{"h1", "h2", "h3"}); err != nil {
		t.Fatalf("batch write: %v", err)
	}

	// all three vectorised → no NULL embeddings.
	if n, err := s.CountNullEmbeddings(ctx); err != nil || n != 0 {
		t.Fatalf("after batch: null=%d err=%v, want 0", n, err)
	}
	// hashes persisted (resume marker) — the work-list with ResumeOnly is now empty.
	rows, err := s.ClausesNeedingEmbedding(ctx, EmbedScan{ResumeOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		t.Errorf("ResumeOnly returned a row after batch write set all hashes")
	}

	// spot-check one vector round-trips: chunk 2's index 1 == 1.
	var got float32
	if err := s.DB().QueryRowContext(ctx,
		`SELECT embedding[2] FROM clauses WHERE chunk_id = 2`).Scan(&got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got != 1 {
		t.Errorf("chunk 2 embedding[2] = %v, want 1 (DuckDB arrays are 1-indexed)", got)
	}

	// ragged input is rejected.
	if err := s.SetEmbeddingsBatch(ctx, []uint64{1, 2}, [][]float32{v1}, []string{"h"}); err == nil {
		t.Errorf("ragged input accepted, want error")
	}
	// empty input is a no-op.
	if err := s.SetEmbeddingsBatch(ctx, nil, nil, nil); err != nil {
		t.Errorf("empty batch errored: %v", err)
	}
}
