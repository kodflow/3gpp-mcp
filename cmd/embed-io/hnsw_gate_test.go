package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// writeVecs writes a JSONL vector file (the Rust embedder's output shape) covering
// the given chunk ids — each a full-dim placeholder vector.
func writeVecs(t *testing.T, path string, ids []uint64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	vec := make([]float32, denseDim)
	for i := range vec {
		vec[i] = 0.1
	}
	enc := json.NewEncoder(f)
	for _, id := range ids {
		if err := enc.Encode(vecRecord{ChunkID: id, Hash: "h", Vec: vec}); err != nil {
			t.Fatal(err)
		}
	}
}

func hnswState(t *testing.T, dbPath string) string {
	t.Helper()
	st, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	return st.GetMeta(context.Background(), "hnsw_state")
}

// TestImportBuildHNSWGatedOnCompleteness is the non-regression proof that
// --build-hnsw builds the index ONLY on the final pass (no embeddable clause left
// NULL), not on every partial resume pass — so the resumable Kaggle loop pays one
// O(N) build instead of K. A partial pass must leave the index unbuilt; the final
// pass must freeze it.
func TestImportBuildHNSWGatedOnCompleteness(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "x.duckdb")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertVersion(model.SpecVersion{SpecID: "23.501", Release: "Rel-19", Version: "19.0.0"}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-19", Version: "19.0.0", ClausePath: "1", Heading: "a", Text: "alpha"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-19", Version: "19.0.0", ClausePath: "2", Heading: "b", Text: "beta"},
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	// PARTIAL pass: only clause 1 embedded → clause 2 still NULL → HNSW must be SKIPPED.
	partial := filepath.Join(dir, "partial.jsonl")
	writeVecs(t, partial, []uint64{1})
	if err := runImport(dbPath, partial, "idA", 512, true); err != nil {
		t.Fatal(err)
	}
	if got := hnswState(t, dbPath); got == "frozen" {
		t.Fatalf("HNSW built on a PARTIAL pass (hnsw_state=%q) — the final-pass gate failed", got)
	}

	// FINAL pass: both clauses embedded → no embeddable NULL → HNSW built + frozen.
	full := filepath.Join(dir, "full.jsonl")
	writeVecs(t, full, []uint64{1, 2})
	if err := runImport(dbPath, full, "idA", 512, true); err != nil {
		t.Fatal(err)
	}
	if got := hnswState(t, dbPath); got != "frozen" {
		t.Fatalf("HNSW NOT frozen on the FINAL pass (hnsw_state=%q)", got)
	}
}
