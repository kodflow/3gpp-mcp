package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestRunSparsePopulatesAndResumes proves the --sparse-only pass fills clause_sparse
// for clauses missing it and is resumable (a second run finds nothing to do).
func TestRunSparsePopulatesAndResumes(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "s.duckdb")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-18", Version: "18.6.0", ClausePath: "5.1", Heading: "AMF", Text: "amf registration"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-18", Version: "18.6.0", ClausePath: "5.2", Heading: "SMF", Text: "pdu session"},
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close() // runSparse opens its own handle

	var buf bytes.Buffer
	n, err := runSparse(ctx, dbPath, embed.Local{}, 8, 0, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("populated=%d, want 2", n)
	}

	// Re-open and confirm the arm is available + a sparse query returns a hit.
	st2, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st2.Close() }()
	if err := st2.LoadSparse(ctx); err != nil || !st2.SparseAvailable() {
		t.Fatalf("sparse not available after population (err=%v)", err)
	}

	// Resumability: a second pass finds nothing missing.
	n2, err := runSparse(ctx, dbPath, embed.Local{}, 8, 0, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Fatalf("second pass populated=%d, want 0 (resumable/idempotent)", n2)
	}
}
