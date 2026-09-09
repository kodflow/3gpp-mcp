package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestRepairKeepsTheFirstBlockAndTheDocumentsOwnRepeats is the safety property
// this tool lives or dies by.
//
// A document may legitimately repeat a clause — ETSI EN 300 607-1 is a GSM test
// spec whose tables restate the same numbered step, and the published corpus
// holds 831 192 such repeats. Every copy of a re-ingested document repeats them
// too, so "keep one row per distinct clause" would delete real content.
//
// Each ingest appended a contiguous block of chunk_ids, so the repair keeps the
// lowest N/k and drops the rest. The fixture below is a two-clause document whose
// SECOND clause appears twice within one write, ingested three times: the correct
// result is 3 rows, not 2, and the survivors must be the first block.
func TestRepairKeepsTheFirstBlockAndTheDocumentsOwnRepeats(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "c.duckdb")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	db := st.DB()

	// One body per distinct (heading, text); the repair must never touch these.
	for _, q := range []string{
		`INSERT INTO bodies (body_id, heading) VALUES (1, 'Scope'), (2, 'Step')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	// Three writes of a document whose "Step" clause occurs twice per write.
	// chunk_ids ascend across writes, exactly as max_chunk_id offsetting produces.
	ins := `INSERT INTO clause_occ (chunk_id, spec_id, release, version, clause_path, is_normative, body_id) VALUES (?,?,?,?,?,?,?)`
	id := 1
	for write := 0; write < 3; write++ {
		for _, r := range []struct {
			path string
			body int
		}{{"1", 1}, {"2", 2}, {"2", 2}} {
			if _, err := db.ExecContext(ctx, ins, id, "23.501", "Rel-18", "18.0.0", r.path, true, r.body); err != nil {
				t.Fatal(err)
			}
			id++
		}
	}
	// A second deliverable, written once, that must be left completely alone.
	if _, err := db.ExecContext(ctx, ins, 100, "24.501", "Rel-18", "18.0.0", "1", true, 1); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	if err := run(dbPath, true); err != nil {
		t.Fatalf("repair: %v", err)
	}

	ck, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ck.Close() }()

	var n int
	if err := ck.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM clause_occ WHERE spec_id = '23.501'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("kept %d occurrence(s), want 3 — one write of a document whose second "+
			"clause legitimately occurs twice", n)
	}
	// The survivors must be the FIRST block, 1..3.
	var lo, hi int
	if err := ck.DB().QueryRowContext(ctx,
		`SELECT min(chunk_id), max(chunk_id) FROM clause_occ WHERE spec_id = '23.501'`).Scan(&lo, &hi); err != nil {
		t.Fatal(err)
	}
	if lo != 1 || hi != 3 {
		t.Errorf("survivors are chunk_ids %d..%d, want 1..3 — the first block is the one to keep", lo, hi)
	}
	// The repeat inside that block must have survived.
	if err := ck.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM clause_occ WHERE spec_id = '23.501' AND clause_path = '2'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("the document's own repeat was deleted: clause 2 has %d row(s), want 2", n)
	}
	// The untouched deliverable is untouched.
	if err := ck.DB().QueryRowContext(ctx,
		`SELECT count(*) FROM clause_occ WHERE spec_id = '24.501'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("a deliverable written once was modified: %d row(s), want 1", n)
	}
	// Bodies are shared and must be left alone: the text and the vectors live there.
	if err := ck.DB().QueryRowContext(ctx, `SELECT count(*) FROM bodies`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("the repair touched bodies (%d rows, want 2) — text and embeddings live there", n)
	}
}

// TestRepairIsIdempotent: a second run must find nothing. A repair that keeps
// finding work is one that did not finish.
func TestRepairIsIdempotent(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "c.duckdb")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO bodies (body_id, heading) VALUES (1, 'Scope')`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 4; i++ {
		if _, err := st.DB().ExecContext(ctx,
			`INSERT INTO clause_occ (chunk_id, spec_id, release, version, clause_path, is_normative, body_id) VALUES (?,?,?,?,?,?,?)`,
			i, "23.501", "Rel-18", "18.0.0", "1", true, 1); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()

	if err := run(dbPath, true); err != nil {
		t.Fatal(err)
	}
	groupsAfter := func() int {
		ck, err := store.OpenReadOnly(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ck.Close() }()
		g, err := ck.ReingestedOccurrences(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return len(g)
	}
	if n := groupsAfter(); n != 0 {
		t.Fatalf("after the repair, %d group(s) still look re-ingested", n)
	}
	if err := run(dbPath, true); err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	if n := groupsAfter(); n != 0 {
		t.Errorf("a second repair found %d group(s)", n)
	}
}
