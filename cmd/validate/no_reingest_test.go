package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestRequireNoReingestFindsTheWholeDocumentWrittenTwice pins the gate that was
// missing, and the shape of the defect it exists for.
//
// require-worklist asks whether anything is MISSING from the corpus. Nothing was —
// so it stayed green while the ETSI half gained 566 clauses on every build from a
// converted tree that never changed. A corpus can be wrong by holding too MUCH,
// and no check looked in that direction.
//
// THE PREDICATE MUST NOT FIRE ON A DOCUMENT THAT REPEATS ITSELF. ETSI EN 300 607-1
// is a GSM test spec whose tables restate the same numbered step, and the
// published corpus holds 831 192 such repeats across 3.1 M rows. What cannot
// happen legitimately is EVERY distinct row of one (spec, version) appearing at
// least twice. Measured over the published ETSI corpus, 11 826 versions: the test
// selects exactly two — TR 104 066 v1.1.1 and TS 103 634 v1.1.1, both at 15
// copies — and nothing else.
func TestRequireNoReingestFindsTheWholeDocumentWrittenTwice(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "c.duckdb")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS"})
	_ = st.UpsertSpec(model.Spec{SpecID: "30.531", Series: "30", DocType: "TR"})

	rows := []model.Clause{
		// A clean deliverable: each row once.
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "1", Text: "scope"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "2", Text: "refs"},
		// A document that legitimately repeats ONE row — the EN 300 607-1 shape.
		// It must NOT be reported: some row of it still appears exactly once.
		{ChunkID: 3, SpecID: "23.501", Release: "Rel-19", Version: "19.0.0", ClausePath: "4.1", Text: "step"},
		{ChunkID: 4, SpecID: "23.501", Release: "Rel-19", Version: "19.0.0", ClausePath: "4.1", Text: "step"},
		{ChunkID: 5, SpecID: "23.501", Release: "Rel-19", Version: "19.0.0", ClausePath: "4.2", Text: "once"},
		// The whole document written twice — this IS the defect.
		{ChunkID: 6, SpecID: "30.531", Release: "Rel-17", Version: "1.62.0", ClausePath: "1", Text: "a"},
		{ChunkID: 7, SpecID: "30.531", Release: "Rel-17", Version: "1.62.0", ClausePath: "2", Text: "b"},
		{ChunkID: 8, SpecID: "30.531", Release: "Rel-17", Version: "1.62.0", ClausePath: "1", Text: "a"},
		{ChunkID: 9, SpecID: "30.531", Release: "Rel-17", Version: "1.62.0", ClausePath: "2", Text: "b"},
	}
	if err := st.InsertClauses(rows); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	db, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	res := result{OK: true}
	checkNoReingest(ctx, db.DB(), &res)
	if len(res.Checks) != 1 {
		t.Fatalf("want one check, got %d", len(res.Checks))
	}
	got := res.Checks[0]
	if got.Pass {
		t.Fatalf("a deliverable written twice must fail the gate: %s", got.Detail)
	}
	if !strings.Contains(got.Detail, "30.531 v1.62.0") {
		t.Errorf("the failure must name the offender: %s", got.Detail)
	}
	if !strings.Contains(got.Detail, "2 copies") {
		t.Errorf("the multiplicity IS the diagnosis — 2 here, 15 on the real corpus: %s", got.Detail)
	}
	if strings.Contains(got.Detail, "23.501") {
		t.Errorf("a document that legitimately repeats one row was reported: %s", got.Detail)
	}
	if !strings.Contains(got.Detail, "2 excess row(s)") {
		t.Errorf("the excess must be counted, not just the group: %s", got.Detail)
	}
}

// TestRequireNoReingestPassesOnACleanCorpus keeps the gate from being one that
// cannot go green: a check that always fires is one people learn to ignore.
func TestRequireNoReingestPassesOnACleanCorpus(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "clean.duckdb")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS"})
	if err := st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "1", Text: "scope"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "4.1", Text: "step"},
		{ChunkID: 3, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "4.1", Text: "step"},
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	db, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	res := result{OK: true}
	checkNoReingest(ctx, db.DB(), &res)
	if !res.Checks[0].Pass {
		t.Errorf("a clean corpus that merely repeats a row must pass: %s", res.Checks[0].Detail)
	}
}
