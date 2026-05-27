package store

import (
	"context"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestCountNullEmbeddings is the authoritative post-ingest check that vectors
// were actually populated (st.Vectors is only a write-side flag).
func TestCountNullEmbeddings(t *testing.T) {
	ctx := context.Background()
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "1", Heading: "a", Text: "x"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "2", Heading: "b", Text: "y"},
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := st.CountNullEmbeddings(ctx); err != nil || n != 2 {
		t.Fatalf("before embed: want (2,nil), got (%d,%v)", n, err)
	}
	if err := st.SetEmbedding(ctx, 1, make([]float32, 1024)); err != nil {
		t.Fatal(err)
	}
	if n, err := st.CountNullEmbeddings(ctx); err != nil || n != 1 {
		t.Fatalf("after embedding chunk 1: want (1,nil), got (%d,%v)", n, err)
	}
}
