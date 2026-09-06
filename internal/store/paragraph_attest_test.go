package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/marcboeker/go-duckdb/v2"
)

// attestFixture builds the smallest corpus the attestation cares about: the four
// content-addressed tables and schema_meta.
func attestFixture(t *testing.T, occ int) *sql.DB {
	t.Helper()
	h, err := sql.Open("duckdb", filepath.Join(t.TempDir(), "attest.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	for _, q := range []string{
		`CREATE TABLE schema_meta (key VARCHAR PRIMARY KEY, value VARCHAR)`,
		`CREATE TABLE paragraphs (para_id INTEGER, part VARCHAR)`,
		`CREATE TABLE bodies (body_id INTEGER, heading VARCHAR)`,
		`CREATE TABLE body_seq (body_id INTEGER, ord SMALLINT, para_id INTEGER)`,
		`CREATE TABLE clause_occ (chunk_id UBIGINT, body_id INTEGER)`,
	} {
		if _, err := h.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	// A converted corpus serves `clauses` as the VIEW over these tables — the
	// attestation checks that shape, so the fixture has to carry it.
	if _, err := h.Exec(`CREATE VIEW clauses AS SELECT chunk_id, 'x' AS text FROM clause_occ`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < occ; i++ {
		if _, err := h.Exec(`INSERT INTO clause_occ VALUES (?, 1)`, i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Exec(`INSERT INTO paragraphs VALUES (1, 'a'), (2, 'b')`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Exec(`INSERT INTO bodies VALUES (1, 'h')`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Exec(`INSERT INTO body_seq VALUES (1, 0, 1), (1, 1, 2)`); err != nil {
		t.Fatal(err)
	}
	return h
}

// An un-attested corpus must be REFUSED, not assumed good. This is what makes
// the migration path work: the step runs, proves itself once, and stamps.
func TestAnUnattestedCorpusIsRefused(t *testing.T) {
	h := attestFixture(t, 3)
	err := CheckParagraphAttestation(h)
	if err == nil {
		t.Fatal("a corpus that was never verified passed the cheap check")
	}
	if !errors.As(err, &ErrNoParagraphAttestation{}) {
		t.Fatalf("want a missing-attestation error the caller can act on, got %v", err)
	}
}

// The happy path, and the property the whole trade rests on: stamp after a
// proof, and the cheap check agrees.
func TestStampThenCheckAgrees(t *testing.T) {
	h := attestFixture(t, 3)
	c, err := StampParagraphAttestation(h)
	if err != nil {
		t.Fatal(err)
	}
	if c.ClauseOcc != 3 || c.Paragraphs != 2 || c.Bodies != 1 || c.BodySeq != 2 {
		t.Fatalf("counters read wrong: %s", c)
	}
	if err := CheckParagraphAttestation(h); err != nil {
		t.Fatalf("a freshly attested corpus failed its own check: %v", err)
	}
}

// THE NEGATIVE CONTROL, one case per counter. A cheap check is only worth having
// if it actually notices the corpus moving; a check that passes on a changed
// corpus is worse than no check, because it looks like one.
func TestAnyChangedCounterFailsTheCheck(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"an occurrence disappeared", `DELETE FROM clause_occ WHERE chunk_id = 0`},
		{"an occurrence appeared", `INSERT INTO clause_occ VALUES (99, 1)`},
		{"a paragraph disappeared", `DELETE FROM paragraphs WHERE para_id = 1`},
		{"a body appeared", `INSERT INTO bodies VALUES (2, 'h2')`},
		{"a sequence row disappeared", `DELETE FROM body_seq WHERE ord = 1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := attestFixture(t, 3)
			if _, err := StampParagraphAttestation(h); err != nil {
				t.Fatal(err)
			}
			if _, err := h.Exec(tc.sql); err != nil {
				t.Fatal(err)
			}
			if err := CheckParagraphAttestation(h); err == nil {
				t.Fatalf("%s did not fail the check — a stale attestation would be trusted", tc.name)
			}
		})
	}
}

// An empty corpus must never be attestable. `paragraphs` exists because a corpus
// that lost its occurrences still opens and still answers queries — with nothing
// in it — and an attestation would make that state look proven.
func TestAnEmptyCorpusCannotBeAttested(t *testing.T) {
	h := attestFixture(t, 0)
	if _, err := StampParagraphAttestation(h); err == nil {
		t.Fatal("a corpus with zero occurrences was attested")
	}
	if err := CheckParagraphAttestation(h); err == nil {
		t.Fatal("an empty corpus passed the cheap check")
	}
}

// Re-stamping after real work must move the attestation, or the second
// conversion would be judged against the first one's shape.
func TestReStampingTracksTheNewShape(t *testing.T) {
	h := attestFixture(t, 3)
	if _, err := StampParagraphAttestation(h); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Exec(`INSERT INTO clause_occ VALUES (77, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := CheckParagraphAttestation(h); err == nil {
		t.Fatal("the check passed before the new shape was attested")
	}
	if _, err := StampParagraphAttestation(h); err != nil {
		t.Fatal(err)
	}
	if err := CheckParagraphAttestation(h); err != nil {
		t.Fatalf("re-stamping did not take: %v", err)
	}
}

// THE INTERRUPTED RESTORE, which counters alone cannot see.
//
// `--restore` puts `clauses` back as a real table and THEN removes the
// content-addressed tables. Killed in between, it leaves an EMPTY `clauses`
// table while all four counters still match the attestation exactly. A check
// built on counters alone would call that corpus verified, the pipeline would
// skip the repair, and every search would read the empty table.
//
// cmd/migrate-paragraphs ships --repair-view specifically to recover this state,
// which is the evidence that it happens.
func TestAnInterruptedRestoreFailsTheCheck(t *testing.T) {
	h := attestFixture(t, 3)
	if _, err := StampParagraphAttestation(h); err != nil {
		t.Fatal(err)
	}
	if err := CheckParagraphAttestation(h); err != nil {
		t.Fatalf("the fixture must pass before the damage: %v", err)
	}

	// The signature of a killed restore: clauses is a TABLE, and empty, while the
	// content-addressed tables are untouched.
	if _, err := h.Exec(`DROP VIEW clauses`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Exec(`CREATE TABLE clauses (chunk_id UBIGINT, text VARCHAR)`); err != nil {
		t.Fatal(err)
	}

	err := CheckParagraphAttestation(h)
	if err == nil {
		t.Fatal("an interrupted restore passed the check — the pipeline would skip the repair " +
			"and serve an empty corpus")
	}
	if !strings.Contains(err.Error(), "repair-view") {
		t.Fatalf("the error must point at the recovery it needs, got: %v", err)
	}
}

// And the converse: the VIEW a converted corpus carries must pass.
func TestTheConvertedShapePasses(t *testing.T) {
	h := attestFixture(t, 3)
	if _, err := StampParagraphAttestation(h); err != nil {
		t.Fatal(err)
	}
	if err := CheckParagraphAttestation(h); err != nil {
		t.Fatalf("a converted corpus failed its own check: %v", err)
	}
}
