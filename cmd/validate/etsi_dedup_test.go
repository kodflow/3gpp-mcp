package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestETSIDedupGateFiresOnlyWhenVersionsActuallyREPEAT pins a gate that has to be
// wrong in BOTH directions to be useless, so it is tested in both.
//
// Store.SearchClauses sends a content-addressed corpus to searchClausesCA, which
// collapses to one row per (spec_id, clause_path). Anything else keeps every
// occurrence as its own candidate — and the comment on that branch records the
// consequence: "the whole twelve-hit window for CHECK_IMEI was one clause repeated
// across twelve releases, and the spec that answers it never entered the window".
//
// The ETSI half is unconverted. That is harmless while its crawl keeps ONE version
// per deliverable, because with one version there is nothing to repeat. So the gate
// must NOT fire on that corpus: failing on correct data is the worse kind of gate,
// the kind that teaches the operator to pass --skip. It must fire the moment a
// second version of the same clause lands, which is exactly what keeping every
// published version does.
func TestETSIDedupGateFiresOnlyWhenVersionsActuallyREPEAT(t *testing.T) {
	ctx := context.Background()
	const identity = "38067f8c6efe"
	main3gpp := seedIndexedCorpus(t, "3gpp.duckdb", identity)

	// ONE version per deliverable — today's ETSI corpus. Unconverted, and fine.
	single := seedETSIVersions(t, identity, "1.20.1")
	res := runChecks(ctx, checkCfg{db: main3gpp, requireETSI: single})
	if !res.OK {
		t.Fatalf("a single-version ETSI half must PASS unconverted, got %+v", res.Checks)
	}
	if detail, found := detailOf(res, "etsi-dedup"); !found {
		t.Fatal("the dedup gate did not run at all on an unconverted corpus")
	} else if !strings.Contains(detail, "1 version(s)") {
		t.Errorf("expected the gate to report one version, got %q", detail)
	}

	// The ONLY change: the SAME clause at a SECOND version, the way keeping every
	// published version puts it there. Vectors, identity and the index are built
	// the same way, so nothing this gate's neighbours ask about has moved — and
	// they still pass, which is what makes the failure below attributable.
	repeated := seedETSIVersions(t, identity, "1.20.1", "1.21.1")
	res = runChecks(ctx, checkCfg{db: main3gpp, requireETSI: repeated})
	if res.OK {
		t.Fatal("validate passed an unconverted ETSI half holding the same clause at two versions — " +
			"that corpus is served from the branch that ranks versions, and the image would ship it")
	}
	for _, c := range res.Checks {
		if c.Name == "require-etsi" && !c.Pass {
			t.Fatalf("require-etsi also failed (%s): the dedup failure is not attributable", c.Detail)
		}
	}
	detail, _ := detailOf(res, "etsi-dedup")
	if !strings.Contains(detail, "2 version(s)") || !strings.Contains(detail, "migrate-paragraphs") {
		t.Errorf("the gate must name the repetition and the remedy, got %q", detail)
	}
}

// seedETSIVersions writes an ETSI-shaped corpus carrying ONE clause at each of the
// given versions: same spec, same clause_path, same text — which is what successive
// versions of a deliverable overwhelmingly are, and the reason an unconverted
// corpus ranks them against each other instead of collapsing them.
//
// The clauses go in BEFORE the index is frozen. Inserting into a table that
// already carries an HNSW index needs the vss extension loaded on that connection,
// and store.Open does not load it — a reopen-and-insert fails with "unknown index
// type 'HNSW'" rather than doing what the test meant.
func seedETSIVersions(t *testing.T, identity string, versions ...string) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "etsi.duckdb")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var rows []model.Clause
	for i, v := range versions {
		rows = append(rows, model.Clause{
			ChunkID: uint64(i + 1), SpecID: "103.221-1", Release: "ETSI", Version: v,
			ClausePath: "6.2.1", Heading: "X1 interface", Text: "the ADMF activates a task",
		})
	}
	if err := db.InsertClauses(rows); err != nil {
		t.Fatal(err)
	}
	for i := range rows {
		if err := db.SetEmbedding(ctx, uint64(i+1), make([]float32, 1024)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.BuildAndFreezeHNSW(ctx, identity); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	return path
}

func detailOf(res result, name string) (string, bool) {
	for _, c := range res.Checks {
		if c.Name == name {
			return c.Detail, true
		}
	}
	return "", false
}
