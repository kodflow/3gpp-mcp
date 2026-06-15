package main

import (
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// seedCheckDB writes a tiny un-embedded, un-indexed DB for the check-data gate.
func seedCheckDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cd.duckdb")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-18", Version: "18.6.0", ClausePath: "1", Heading: "h", Text: "t"},
		{ChunkID: 2, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "2", Heading: "h", Text: "t"},
	}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	return path
}

// TestCheckDataContract pins the extended contract on the image-build guard: dense
// convergence is floor-aware (a high floor excludes the seed clauses → passes; no
// floor counts the NULL embeddings → fails), mirroring cmd/validate. FTS/HNSW are
// disabled here so the test isolates the new --require-embed-complete behaviour.
func TestCheckDataContract(t *testing.T) {
	db := seedCheckDB(t)

	// Below-floor exclusion: floor Rel-20 leaves no seed clause at/above → PASS.
	if err := checkData([]string{"--db", db, "--require-fts=false", "--require-hnsw=false", "--require-embed-complete", "--embed-floor", "Rel-20"}); err != nil {
		t.Errorf("check-data require-embed-complete at floor Rel-20 should pass, got %v", err)
	}
	// No floor: the two clauses are un-embedded → dense incomplete → FAIL.
	if err := checkData([]string{"--db", db, "--require-fts=false", "--require-hnsw=false", "--require-embed-complete"}); err == nil {
		t.Error("check-data require-embed-complete on an un-embedded DB should fail, got nil")
	}
}
