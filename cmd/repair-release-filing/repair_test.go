package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

// fixture builds a corpus that carries, in miniature, every shape the published
// one does. The numbers are the real ones where they matter.
//
//	26.510  Rel-18 18.4.0  (bookkeeping, no text, NO archive URL)
//	        Rel-18 18.5.0  2 occurrences
//	        Rel-20 18.4.0  2 occurrences  <- mis-filed, MOVES to Rel-18
//	24.283  Rel-19 19.2.0  1 occurrence
//	        Rel-20 19.1.0  1 occurrence   <- mis-filed, MOVES, filing created
//	21.810  Rel-4  3.0.0   1 occurrence   <- carried forward, no Rel-99: KEEPS
//	33.816  Rel-10 10.0.0  1 occurrence
//	        Rel-11 10.0.0  1 occurrence   <- both copies real: KEEPS
//	36.833  Rel-13 0.4.0   1 occurrence   <- draft: not even a candidate
//	29.675  Rel-20 19.2.0  (bookkeeping, no text): KEEPS
func fixture(t *testing.T) string {
	t.Helper()
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
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO bodies (body_id, heading) VALUES (1, 'Scope'), (2, 'Body')`)

	sv := `INSERT INTO spec_versions (spec_id, release, version, docx_url) VALUES (?,?,?,?)`
	exec(sv, "26.510", "Rel-18", "18.4.0", "") // the bookkeeping row with no archive
	exec(sv, "26.510", "Rel-18", "18.5.0", "https://x/26510-i50.zip")
	exec(sv, "26.510", "Rel-20", "18.4.0", "https://x/26510-i40.zip")
	exec(sv, "24.283", "Rel-19", "19.2.0", "https://x/24283-j20.zip")
	exec(sv, "24.283", "Rel-20", "19.1.0", "https://x/24283-j10.zip")
	exec(sv, "21.810", "Rel-4", "3.0.0", "https://x/21810-300.zip")
	exec(sv, "33.816", "Rel-10", "10.0.0", "https://x/33816-a00.zip")
	exec(sv, "33.816", "Rel-11", "10.0.0", "https://x/33816-a00.zip")
	exec(sv, "36.833", "Rel-13", "0.4.0", "https://x/36833-040.zip")
	exec(sv, "29.675", "Rel-20", "19.2.0", "https://x/29675-j20.zip")

	occ := `INSERT INTO clause_occ (chunk_id, spec_id, release, version, clause_path, is_normative, body_id) VALUES (?,?,?,?,?,?,?)`
	id := 1
	add := func(spec, rel, ver string, n int) {
		for i := 0; i < n; i++ {
			exec(occ, id, spec, rel, ver, "1", true, 1+i%2)
			id++
		}
	}
	add("26.510", "Rel-18", "18.5.0", 2)
	add("26.510", "Rel-20", "18.4.0", 2)
	add("24.283", "Rel-19", "19.2.0", 1)
	add("24.283", "Rel-20", "19.1.0", 1)
	add("21.810", "Rel-4", "3.0.0", 1)
	add("33.816", "Rel-10", "10.0.0", 1)
	add("33.816", "Rel-11", "10.0.0", 1)
	add("36.833", "Rel-13", "0.4.0", 1)
	// 29.675 Rel-20 deliberately has none.
	_ = st.Close()
	return dbPath
}

func apply(t *testing.T, dbPath string, write bool) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	if err := run(dbPath, write, f); err != nil {
		t.Fatalf("run(apply=%v): %v", write, err)
	}
	_ = f.Close()
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func rows(t *testing.T, dbPath, q string) [][]string {
	t.Helper()
	st, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	rs, err := st.DB().QueryContext(context.Background(), q)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer func() { _ = rs.Close() }()
	cols, err := rs.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for rs.Next() {
		cells := make([]any, len(cols))
		raw := make([]sql.NullString, len(cols))
		for i := range cells {
			cells[i] = &raw[i]
		}
		if err := rs.Scan(cells...); err != nil {
			t.Fatal(err)
		}
		line := make([]string, len(cols))
		for i, v := range raw {
			line[i] = v.String
		}
		out = append(out, line)
	}
	return out
}

func joined(rr [][]string) []string {
	out := make([]string, 0, len(rr))
	for _, r := range rr {
		out = append(out, strings.Join(r, " "))
	}
	return out
}

func eq(t *testing.T, got, want []string, what string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

// THE WHOLE POINT: the text ends up under the release its version names, and the
// document that had no copy of 18.4.0 anywhere now has one where a reader asking
// for Rel-18 will find it.
func TestTheTextMovesToTheReleaseItsVersionNames(t *testing.T) {
	db := fixture(t)
	before := joined(rows(t, db, `SELECT release, version, count(*)::VARCHAR FROM clause_occ WHERE spec_id='26.510' GROUP BY 1,2 ORDER BY 1,2`))
	eq(t, before, []string{"Rel-18 18.5.0 2", "Rel-20 18.4.0 2"}, "before")

	out := apply(t, db, true)
	if !strings.Contains(out, "MOVE 26.510      Rel-20  18.4.0    -> Rel-18") {
		t.Errorf("the move must be named in the report; got:\n%s", out)
	}
	after := joined(rows(t, db, `SELECT release, version, count(*)::VARCHAR FROM clause_occ WHERE spec_id='26.510' GROUP BY 1,2 ORDER BY 1,2`))
	eq(t, after, []string{"Rel-18 18.4.0 2", "Rel-18 18.5.0 2"}, "after")
}

// NOTHING IS DELETED, AND NOTHING IS LOST. The occurrence total is the property
// the whole repair lives or dies by: a move that loses a row is a deletion with a
// better name. The carrying spec_versions row stays, as the bookkeeping the
// catalogue wrote.
func TestItMovesAndNeverDeletes(t *testing.T) {
	db := fixture(t)
	occBefore := rows(t, db, `SELECT count(*)::VARCHAR FROM clause_occ`)[0][0]
	svBefore := rows(t, db, `SELECT count(*)::VARCHAR FROM spec_versions`)[0][0]

	apply(t, db, true)

	occAfter := rows(t, db, `SELECT count(*)::VARCHAR FROM clause_occ`)[0][0]
	if occAfter != occBefore {
		t.Errorf("clause_occ went from %s to %s — the repair lost or invented occurrences", occBefore, occAfter)
	}
	if svBefore != "10" || rows(t, db, `SELECT count(*)::VARCHAR FROM spec_versions`)[0][0] != "11" {
		t.Errorf("spec_versions should gain exactly the one destination filing that did not exist (24.283 Rel-19 19.1.0), got %s -> %s",
			svBefore, rows(t, db, `SELECT count(*)::VARCHAR FROM spec_versions`)[0][0])
	}
	kept := joined(rows(t, db, `SELECT release, version FROM spec_versions WHERE spec_id='26.510' ORDER BY release, version`))
	eq(t, kept, []string{"Rel-18 18.4.0", "Rel-18 18.5.0", "Rel-20 18.4.0"},
		"the carrying row must survive as bookkeeping")
}

// THE ARCHIVE URL MOVES WITH THE TEXT. Six of the twelve filings on the published
// corpus land on a bookkeeping row the catalogue left with no docx_url, while the
// mis-filed row carries the URL the text was downloaded from. Without this the
// served document cannot cite where it came from.
func TestTheArchiveURLMovesWithTheText(t *testing.T) {
	db := fixture(t)
	eq(t, joined(rows(t, db, `SELECT COALESCE(docx_url,'') FROM spec_versions WHERE spec_id='26.510' AND release='Rel-18' AND version='18.4.0'`)),
		[]string{""}, "the destination starts with no archive")

	apply(t, db, true)

	eq(t, joined(rows(t, db, `SELECT COALESCE(docx_url,'') FROM spec_versions WHERE spec_id='26.510' AND release='Rel-18' AND version='18.4.0'`)),
		[]string{"https://x/26510-i40.zip"}, "the destination must name the archive the text came from")
	// A destination that already names an archive is never overwritten.
	eq(t, joined(rows(t, db, `SELECT COALESCE(docx_url,'') FROM spec_versions WHERE spec_id='26.510' AND release='Rel-18' AND version='18.5.0'`)),
		[]string{"https://x/26510-i50.zip"}, "an existing archive URL is left alone")
}

// EACH REFUSAL IS DOING WORK. Left alone: the carried-forward Rel-99 document
// whose only home is Rel-4, the version filed twice with text under both, the
// draft, and the bookkeeping row with nothing behind it. Getting any of these
// wrong deletes real text or invents a release.
func TestWhatItRefusesToMove(t *testing.T) {
	db := fixture(t)
	out := apply(t, db, true)

	for _, want := range []string{
		"KEEP 21.810      Rel-4   3.0.0        the corpus files 21.810 under no Rel-99",
		"KEEP 33.816      Rel-11  10.0.0       Rel-10 already holds 1 occurrence(s) of 10.0.0",
		"KEEP 29.675      Rel-20  19.2.0       catalogue bookkeeping — no occurrence to move",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report must name the refusal %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "36.833") {
		t.Errorf("a draft is not a candidate at all and must not be reported; got:\n%s", out)
	}
	eq(t, joined(rows(t, db, `SELECT release, version, count(*)::VARCHAR FROM clause_occ WHERE spec_id IN ('21.810','33.816','36.833') GROUP BY 1,2 ORDER BY 1,2`)),
		[]string{"Rel-10 10.0.0 1", "Rel-11 10.0.0 1", "Rel-13 0.4.0 1", "Rel-4 3.0.0 1"},
		"every refused filing must be exactly where it was")
}

// A SECOND PASS MUST WRITE NOTHING. The orchestrator runs this by hand on a 23 GB
// corpus; a tool that shifts bytes on a no-op re-pushes an image layer for
// nothing. It also must not open the corpus read-write to discover it has no work.
func TestASecondPassIsANoOp(t *testing.T) {
	db := fixture(t)
	apply(t, db, true)
	before, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}

	out := apply(t, db, true)
	if !strings.Contains(out, "nothing to move — corpus untouched") {
		t.Errorf("the second pass must say it found nothing; got:\n%s", out)
	}
	after, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("the second pass changed the file: %d bytes -> %d bytes", len(before), len(after))
	}
}

// A DRY RUN REPORTS AND WRITES NOTHING.
func TestADryRunWritesNothing(t *testing.T) {
	db := fixture(t)
	before, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	out := apply(t, db, false)
	if !strings.Contains(out, "DRY RUN — pass --apply to write") {
		t.Errorf("a dry run must say so; got:\n%s", out)
	}
	if !strings.Contains(out, "2 to move") {
		t.Errorf("a dry run must report the work it would do; got:\n%s", out)
	}
	after, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("the dry run changed the file: %d bytes -> %d bytes", len(before), len(after))
	}
}

// THE ATTESTATION SURVIVES. cmd/migrate-paragraphs attests the corpus with four
// row counts; the pipeline's `paragraphs` step checks them on every plan and
// re-verifies the whole 23 GB corpus if they moved. A repair that changes any of
// them costs ~8 minutes on every build after it.
func TestTheParagraphAttestationSurvives(t *testing.T) {
	db := fixture(t)
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	stamped, err := store.StampParagraphAttestation(st.DB())
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	apply(t, db, true)

	ck, err := store.OpenReadOnly(db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ck.Close() }()
	now, err := store.ReadParagraphCounters(ck.DB())
	if err != nil {
		t.Fatal(err)
	}
	if now.String() != stamped.String() {
		t.Errorf("the repair moved a counter the attestation covers:\n  attested %s\n  now      %s",
			stamped.String(), now.String())
	}
}
