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
		JOIN bodies b ON b.heading IS NOT DISTINCT FROM c.heading AND b.body = c.text
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
