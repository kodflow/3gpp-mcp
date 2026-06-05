package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// seedDB writes a tiny 3-release DB; embedded=false leaves embeddings NULL.
func seedDB(t *testing.T, embedded bool) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v.duckdb")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	clauses := []model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-18", Version: "18.5.0", ClausePath: "1", Heading: "h", Text: "verbatim text"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-19", Version: "19.6.0", ClausePath: "2", Heading: "h", Text: "more text"},
		{ChunkID: 3, SpecID: "33.128", Release: "Rel-99", Version: "3.1.0", ClausePath: "3", Heading: "h", Text: "legacy text"},
	}
	if err := db.InsertClauses(clauses); err != nil {
		t.Fatal(err)
	}
	if embedded {
		for _, c := range clauses {
			if err := db.SetEmbeddingWithHash(ctx, c.ChunkID, make([]float32, 1024), "h"); err != nil {
				t.Fatal(err)
			}
		}
	}
	_ = db.Close()
	return path
}

// TestValidateGate locks the safety-critical acceptance criteria (plan §12.3):
// the anti-leak guard, exact release-set matching, and the pending-zero gate.
func TestValidateGate(t *testing.T) {
	ctx := context.Background()

	// Anti-leak: public channel + verbatim clause text MUST fail (#1).
	res := runChecks(ctx, checkCfg{db: seedDB(t, false), repoVisibility: "public", forbidFulltext: true})
	if res.OK {
		t.Error("anti-leak: public + full-text should FAIL, got OK")
	}

	// Private channel + full-text is allowed (#3).
	res = runChecks(ctx, checkCfg{db: seedDB(t, false), repoVisibility: "private", forbidFulltext: true})
	if !res.OK {
		t.Errorf("private + full-text should PASS, got %+v", res.Checks)
	}

	// Exact release set passes; a missing one fails (#7).
	if res = runChecks(ctx, checkCfg{db: seedDB(t, false), expectedReleases: []string{"Rel-18", "Rel-19", "Rel-99"}}); !res.OK {
		t.Errorf("exact release set should PASS, got %+v", res.Checks)
	}
	if res = runChecks(ctx, checkCfg{db: seedDB(t, false), expectedReleases: []string{"Rel-18", "Rel-19", "Rel-99", "Rel-20"}}); res.OK {
		t.Error("missing release should FAIL, got OK")
	}

	// pending-zero: NULL embeddings fail (#4); fully embedded passes.
	if res = runChecks(ctx, checkCfg{db: seedDB(t, false), pendingZero: true}); res.OK {
		t.Error("pending-zero on un-embedded DB should FAIL, got OK")
	}
	if res = runChecks(ctx, checkCfg{db: seedDB(t, true), pendingZero: true}); !res.OK {
		t.Errorf("pending-zero on embedded DB should PASS, got %+v", res.Checks)
	}

	// min-clauses below the floor fails (#8).
	if res = runChecks(ctx, checkCfg{db: seedDB(t, false), minClauses: 999}); res.OK {
		t.Error("min-clauses 999 on a 3-clause DB should FAIL, got OK")
	}

	// empty-meta (B4a): seedDB writes clauses but NO specs rows → every distinct
	// spec id is missing catalog title/WG. max=0 must FAIL; a generous max passes.
	if res = runChecks(ctx, checkCfg{db: seedDB(t, false), emptyMetaGuard: true, maxEmptyMeta: 0}); res.OK {
		t.Error("empty-meta: 2 clause-bearing specs without catalog metadata should FAIL at max=0, got OK")
	}
	if res = runChecks(ctx, checkCfg{db: seedDB(t, false), emptyMetaGuard: true, maxEmptyMeta: 5}); !res.OK {
		t.Errorf("empty-meta: max=5 should PASS on a 2-spec DB, got %+v", res.Checks)
	}
	// A DB whose specs are fully enriched passes even at max=0.
	if res = runChecks(ctx, checkCfg{db: seedEnrichedDB(t), emptyMetaGuard: true, maxEmptyMeta: 0}); !res.OK {
		t.Errorf("empty-meta: fully-enriched DB should PASS at max=0, got %+v", res.Checks)
	}
}

// seedEnrichedDB writes clauses AND complete specs rows (title + working_group)
// for every clause-bearing spec, so the empty-meta guard finds zero gaps.
func seedEnrichedDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "enriched.duckdb")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-19", Version: "19.6.0", ClausePath: "1", Heading: "h", Text: "t"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS", WorkingGroup: "S2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateSpecMeta(context.Background(), "23.501", "System architecture for the 5G System", "TS", "S2"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	return path
}

// TestChecksum verifies the sha256 sidecar comparison.
func TestChecksum(t *testing.T) {
	dir := t.TempDir()
	blob := filepath.Join(dir, "a.zst")
	if err := os.WriteFile(blob, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	// sha256("hello")
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if err := os.WriteFile(blob+".sha256", []byte(want+"  a.zst\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := runChecks(context.Background(), checkCfg{db: seedDB(t, false), zst: blob, sha: blob + ".sha256"})
	for _, c := range res.Checks {
		if c.Name == "checksum" && !c.Pass {
			t.Errorf("checksum should match: %s", c.Detail)
		}
	}
}
