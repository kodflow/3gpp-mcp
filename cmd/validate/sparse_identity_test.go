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

// seedSparseCorpus writes a corpus whose sparse layer is complete and stamped
// with `stamp`.
func seedSparseCorpus(t *testing.T, stamp string) string {
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

// TestRequireSparseRefusesABuildThatCannotCompare pins the condition that made
// --require-sparse inert exactly where it mattered.
//
// The check compares schema_meta.sparse_model against embed.SparseModelID(),
// which reads the ACTIVE registry entry. The default entry is bge-m3, which is
// DENSE-ONLY, so SparseModelID() returns "" — and the old expression
// `SparseAvailable() && (want == "" || got == want)` then collapsed to "there is
// at least one row in clause_sparse". Measured: `embedid --sparse` prints nothing
// under the default registry and b13103bce7ae under EMBED_MODEL=bge-m3-sparse,
// while the DENSE identity is 38067f8c6efe under both.
//
// scripts/local/build-image.sh called the gate BEFORE it exported the baked
// registry, so the one check written to catch a sparse layer scored against
// another model's vocabulary — which fails silently at serve time — never
// compared anything on the path to :latest.
func TestRequireSparseRefusesABuildThatCannotCompare(t *testing.T) {
	ctx := context.Background()

	t.Setenv("EMBED_MODEL", "bge-m3-sparse")
	want := embed.SparseModelID()
	if want == "" {
		t.Fatal("the dual-head registry entry resolves no sparse identity — the premise of this test is gone")
	}

	// A layer stamped by the model this build resolves: PASS.
	good := seedSparseCorpus(t, want)
	if res := runChecks(ctx, checkCfg{db: good, requireSparse: true}); !res.OK {
		t.Fatalf("a correctly stamped sparse layer should PASS, got %+v", res.Checks)
	}

	// A layer stamped by SOMETHING ELSE: FAIL. This is the comparison the gate
	// exists for, and it only happens when an identity could be resolved.
	stale := seedSparseCorpus(t, "0000deadbeef")
	if res := runChecks(ctx, checkCfg{db: stale, requireSparse: true}); res.OK {
		t.Error("a sparse layer stamped by another model should FAIL, got OK")
	}

	// THE REGRESSION: the same stale layer, asked of a build whose active model
	// has no sparse head. The old code returned OK here — it could not compare, so
	// it did not, and said nothing about it.
	t.Setenv("EMBED_MODEL", "bge-m3")
	if embed.SparseModelID() != "" {
		t.Skip("the default registry entry now has a sparse head; this regression cannot be expressed")
	}
	res := runChecks(ctx, checkCfg{db: stale, requireSparse: true})
	if res.OK {
		t.Fatal("--require-sparse passed a build that resolves NO sparse identity — " +
			"the gate checked only that clause_sparse was non-empty")
	}
	var detail string
	for _, c := range res.Checks {
		if c.Name == "require-sparse" {
			detail = c.Detail
		}
	}
	if !strings.Contains(detail, "no sparse identity") {
		t.Errorf("the failure must say the build cannot resolve a sparse identity, got %q", detail)
	}
}
