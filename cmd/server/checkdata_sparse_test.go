package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// seedStampedSparse writes a corpus whose sparse layer is populated and stamped.
func seedStampedSparse(t *testing.T, stamp string) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sp.duckdb")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-18", Version: "18.6.0",
			ClausePath: "6.2.1", Heading: "AMF", Text: "the AMF terminates the N1 interface"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSparse(ctx, 1, model.SparseVec{42: 0.75, 99: 0.25}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetMeta("sparse_model", stamp); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	return path
}

// TestCheckDataRequireSparseNeedsAnIdentityToCompare is the check-data half of
// cmd/validate's TestRequireSparseRefusesABuildThatCannotCompare. The two gates
// are handed the SAME flag string by scripts/data-contract.sh, so a difference
// between them is a hole; `want == "" ||` made both of them wave the comparison
// through under the default (dense-only) registry entry.
func TestCheckDataRequireSparseNeedsAnIdentityToCompare(t *testing.T) {
	t.Setenv("EMBED_MODEL", "bge-m3-sparse")
	want := embed.SparseModelID()
	if want == "" {
		t.Fatal("the dual-head registry entry resolves no sparse identity — the premise of this test is gone")
	}
	base := []string{"--require-fts=false", "--require-hnsw=false", "--require-sparse"}

	good := seedStampedSparse(t, want)
	if err := checkData(append([]string{"--db", good}, base...)); err != nil {
		t.Fatalf("a correctly stamped sparse layer should pass, got %v", err)
	}

	stale := seedStampedSparse(t, "0000deadbeef")
	if err := checkData(append([]string{"--db", stale}, base...)); err == nil {
		t.Error("a sparse layer stamped by another model should fail, got nil")
	}

	// The regression: the same stale layer, asked of a build whose active model
	// has no sparse head. This used to pass.
	t.Setenv("EMBED_MODEL", "bge-m3")
	if embed.SparseModelID() != "" {
		t.Skip("the default registry entry now has a sparse head; this regression cannot be expressed")
	}
	err := checkData(append([]string{"--db", stale}, base...))
	if err == nil {
		t.Fatal("check-data passed --require-sparse on a build that resolves NO sparse identity")
	}
	if !strings.Contains(err.Error(), "NO sparse identity") {
		t.Errorf("the failure must say the build cannot resolve a sparse identity, got %q", err)
	}
}
