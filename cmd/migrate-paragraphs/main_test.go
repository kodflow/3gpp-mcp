package main

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/marcboeker/go-duckdb/v2"
)

// fixture builds a throwaway corpus whose clauses cover the cases that decide
// whether the split is reversible. Each row is here because getting it wrong
// loses text silently.
func fixture(t *testing.T) *sql.DB {
	t.Helper()
	h, err := sql.Open("duckdb", filepath.Join(t.TempDir(), "fixture.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	if _, err := h.Exec(`CREATE TABLE clauses (
		chunk_id UBIGINT, spec_id VARCHAR, release VARCHAR, version VARCHAR,
		clause_path VARCHAR, heading VARCHAR, text VARCHAR, is_normative BOOLEAN,
		embedding FLOAT[1024], embedding_hash VARCHAR)`); err != nil {
		t.Fatal(err)
	}
	rows := []struct{ spec, rel, head, text string }{
		{"23.501", "Rel-17", "AMF registration", "para one\n\npara two"},
		{"23.501", "Rel-18", "AMF registration", "para one\n\npara two"}, // verbatim repeat across releases
		{"23.501", "Rel-19", "AMF registration", "para one\n\npara three"},
		{"23.502", "Rel-18", "Other heading", "para one\n\npara two"}, // same text, other heading
		{"23.503", "Rel-18", "Blank runs", "a\n\n\n\nb"},              // empty part in the middle
		{"23.504", "Rel-18", "Trailing", "x\n\n"},                     // empty part at the end
		{"23.505", "Rel-18", "Empty body", ""},                        // no text at all
		{"23.506", "Rel-18", "No separator", "single line only"},      // never splits
	}
	for i, r := range rows {
		if _, err := h.Exec(`INSERT INTO clauses VALUES (?,?,?,?,?,?,?,true,NULL,?)`,
			i+1, r.spec, r.rel, "18.0.0", "5.2", r.head, r.text, "hash-"+r.head); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func TestEveryBodyRebuildsByteForByte(t *testing.T) {
	h := fixture(t)
	if err := build(h); err != nil {
		t.Fatal(err)
	}
	if err := verify(h); err != nil {
		t.Fatalf("verification must pass on the fixture: %v", err)
	}
	// Not just the aggregate: compare each occurrence's text to the original.
	rows, err := h.Query(`
		WITH rebuilt AS (
			SELECT s.body_id, string_agg(p.part, chr(10)||chr(10) ORDER BY s.ord) AS body
			FROM body_seq s JOIN paragraphs p USING (para_id) GROUP BY s.body_id
		)
		SELECT c.spec_id, c.text, r.body
		FROM clauses c
		JOIN clause_occ o ON o.chunk_id = c.chunk_id
		JOIN rebuilt r USING (body_id)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var spec, orig, got string
		if err := rows.Scan(&spec, &orig, &got); err != nil {
			t.Fatal(err)
		}
		if orig != got {
			t.Errorf("%s: rebuilt %q, want %q", spec, got, orig)
		}
		n++
	}
	if n != 8 {
		t.Fatalf("compared %d occurrences, want 8", n)
	}
}

// The dedup key is (heading, text). Two clauses with the same body under
// different headings are different bodies, because embedding_hash and BM25 both
// fold the heading in — collapsing them would leave the vectors disagreeing
// with what is served.
func TestTheHeadingIsPartOfTheKey(t *testing.T) {
	h := fixture(t)
	if err := build(h); err != nil {
		t.Fatal(err)
	}
	var bodies, occ int
	if err := h.QueryRow(`SELECT (SELECT count(*) FROM bodies), (SELECT count(*) FROM clause_occ)`).Scan(&bodies, &occ); err != nil {
		t.Fatal(err)
	}
	if occ != 8 {
		t.Fatalf("occurrences = %d, want 8 — every clause must keep a row", occ)
	}
	// 8 clauses, one verbatim repeat across releases -> 7 bodies. If the heading
	// were ignored, "para one\n\npara two" under two headings would collapse to 6.
	if bodies != 7 {
		t.Fatalf("bodies = %d, want 7", bodies)
	}
}

// A run of blank lines produces EMPTY parts. Dropping them looks harmless and is
// the one thing that makes the split irreversible.
func TestEmptyPartsAreKept(t *testing.T) {
	h := fixture(t)
	if err := build(h); err != nil {
		t.Fatal(err)
	}
	var empties int
	if err := h.QueryRow(`SELECT count(*) FROM paragraphs WHERE part = ''`).Scan(&empties); err != nil {
		t.Fatal(err)
	}
	if empties == 0 {
		t.Fatal("the empty paragraph is not stored — 'a\n\n\n\nb' can no longer be rebuilt")
	}
}

// The lineage the whole change exists to serve: one clause, three releases, and
// a body that stops being used after Rel-18.
func TestOccurrencesCarryTheReleaseLineage(t *testing.T) {
	h := fixture(t)
	if err := build(h); err != nil {
		t.Fatal(err)
	}
	rows, err := h.Query(`
		SELECT b.body_id, string_agg(o.release, ',' ORDER BY o.release) AS rels
		FROM clause_occ o JOIN bodies b USING (body_id)
		WHERE o.spec_id = '23.501' AND o.clause_path = '5.2'
		GROUP BY b.body_id ORDER BY rels`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var id int
		var rels string
		if err := rows.Scan(&id, &rels); err != nil {
			t.Fatal(err)
		}
		got = append(got, rels)
	}
	want := "Rel-17,Rel-18|Rel-19"
	if strings.Join(got, "|") != want {
		t.Fatalf("lineage = %q, want %q — the body shared by 17 and 18 must be one row, and Rel-19's a different one",
			strings.Join(got, "|"), want)
	}
}

// A verification that cannot fail is worse than none: prove it rejects a corpus
// whose sequence has been tampered with.
func TestVerificationRejectsALossyCorpus(t *testing.T) {
	h := fixture(t)
	if err := build(h); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Exec(`DELETE FROM body_seq WHERE ord = 2`); err != nil {
		t.Fatal(err)
	}
	if err := verify(h); err == nil {
		t.Fatal("verify accepted a corpus with paragraphs removed from the sequences")
	}
}

// (spec_id, release, version, clause_path) LOOKS like the natural key and is
// not one: front matter carries an empty clause_path, and TS 51.010-1 v7.12.0
// alone has 16 509 such rows in the real corpus. A verification joining on it
// compared 550 568 438 pairs for 2.75 M clauses and reported a corruption that
// did not exist. Only chunk_id identifies an occurrence.
func TestOccurrencesAreIdentifiedByChunkIDNotByPath(t *testing.T) {
	h := fixture(t)
	// Three more rows sharing one (spec, release, version, clause_path) — the
	// shape that made the naive join multiply.
	for i, text := range []string{"front matter one", "front matter two", "front matter three"} {
		if _, err := h.Exec(`INSERT INTO clauses VALUES (?,?,?,?,?,?,?,true,NULL,?)`,
			100+i, "51.010-1", "Rel-7", "7.12.0", "", "Preamble", text, "hash-pre"); err != nil {
			t.Fatal(err)
		}
	}
	if err := build(h); err != nil {
		t.Fatal(err)
	}
	if err := verify(h); err != nil {
		t.Fatalf("a corpus with repeated clause paths must still verify: %v", err)
	}
	var occ int
	if err := h.QueryRow(`SELECT count(*) FROM clause_occ`).Scan(&occ); err != nil {
		t.Fatal(err)
	}
	if occ != 11 {
		t.Fatalf("occurrences = %d, want 11 — one per clause, never a product", occ)
	}
	// And the key really is degenerate, so the test is testing something.
	var distinctKeys int
	if err := h.QueryRow(`SELECT count(DISTINCT (spec_id, release, version, clause_path)) FROM clauses`).Scan(&distinctKeys); err != nil {
		t.Fatal(err)
	}
	if distinctKeys >= 11 {
		t.Fatalf("the fixture no longer contains a repeated clause path (%d distinct keys for 11 rows)", distinctKeys)
	}
}

// Dropping the table must leave a VIEW behind, or every caller that reads
// metadata off `clauses` — validate, anchorcheck, split, the sparse join —
// breaks at once. DuckDB prunes the text column when it is not selected, so
// those callers pay nothing for it.
func TestDroppingTheTableLeavesAWorkingView(t *testing.T) {
	h := fixture(t)
	if err := build(h); err != nil {
		t.Fatal(err)
	}
	if err := verify(h); err != nil {
		t.Fatal(err)
	}
	if err := drop(h); err != nil {
		t.Fatal(err)
	}

	var isView int
	if err := h.QueryRow(`SELECT count(*) FROM duckdb_views() WHERE view_name = 'clauses'`).Scan(&isView); err != nil {
		t.Fatal(err)
	}
	if isView != 1 {
		t.Fatal("`clauses` is gone and nothing replaced it")
	}
	var n int
	if err := h.QueryRow(`SELECT count(*) FROM clauses`).Scan(&n); err != nil {
		t.Fatalf("counting through the view: %v", err)
	}
	if n != 8 {
		t.Fatalf("the view sees %d rows, want 8", n)
	}
	// And the text still comes back when it IS asked for.
	var text string
	if err := h.QueryRow(`SELECT text FROM clauses WHERE spec_id = '23.503'`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text != "a\n\n\n\nb" {
		t.Errorf("view rebuilt %q, want the original blank-run text", text)
	}
}

// Applying the schema to a migrated corpus must not resurrect the table.
func TestTheSchemaDoesNotResurrectTheTable(t *testing.T) {
	h := fixture(t)
	if err := build(h); err != nil {
		t.Fatal(err)
	}
	if err := verify(h); err != nil {
		t.Fatal(err)
	}
	if err := drop(h); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Exec(`CREATE TABLE IF NOT EXISTS clauses (chunk_id UBIGINT, spec_id VARCHAR)`); err != nil {
		t.Fatalf("re-applying the schema over the view failed: %v", err)
	}
	var isView int
	if err := h.QueryRow(`SELECT count(*) FROM duckdb_views() WHERE view_name = 'clauses'`).Scan(&isView); err != nil {
		t.Fatal(err)
	}
	if isView != 1 {
		t.Fatal("the schema replaced the view with an empty table — the corpus would read as empty")
	}
}

// The one way this tool can destroy a corpus: re-deriving from a `clauses` that
// holds only a delta would replace every occurrence with that increment, and
// every count would still agree with itself afterwards.
func TestItRefusesToRebuildFromADelta(t *testing.T) {
	h := fixture(t)
	if err := build(h); err != nil {
		t.Fatal(err)
	}
	// A delta merge: `clauses` now carries one bucket instead of the corpus.
	if _, err := h.Exec(`DELETE FROM clauses WHERE spec_id <> '23.501'`); err != nil {
		t.Fatal(err)
	}
	err := build(h)
	if err == nil {
		t.Fatal("the tool rebuilt from a delta — the corpus would silently shrink to it")
	}
	if !strings.Contains(err.Error(), "REFUSING") {
		t.Errorf("the refusal must be unmistakable, got %q", err)
	}
	// And it must not have touched anything on the way out.
	var occ int
	if err := h.QueryRow(`SELECT count(*) FROM clause_occ`).Scan(&occ); err != nil {
		t.Fatal(err)
	}
	if occ != 8 {
		t.Fatalf("occurrences = %d after a refused rebuild, want the original 8", occ)
	}
}
