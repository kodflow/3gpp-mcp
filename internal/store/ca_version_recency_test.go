package store

import (
	"context"
	"testing"
)

// TestTheHitCitesTheNEWESTVersionCarryingTheText pins the version a search hit is
// LABELLED with when the same text is carried by several versions.
//
// searchClausesCA collapses to one row per (spec_id, clause_path) — that is the
// whole point of the content-addressed path, and it is what keeps a result window
// from filling up with one clause seen from a dozen versions. But collapsing means
// CHOOSING which occurrence gets to speak for the clause, and identical text scores
// identically, so the tie-break decides every time.
//
// The tie-break was a raw `o.version DESC`, which is a STRING sort. store.go
// already carries the counter-example in versionRecencySQL's own doc comment —
// "18.9.0 wrongly sorts AFTER 18.10.0 (9 > 1 lexically)" — and LatestVersion uses
// the numeric helper for exactly that reason. The two content-addressed search
// paths did not, so a corpus whose spec had reached its tenth patch cited a STALE
// version, with the right text and the wrong provenance. On a corpus that keeps
// every published version — ETSI TS 102 221 has 126 — it is not an edge case.
func TestTheHitCitesTheNEWESTVersionCarryingTheText(t *testing.T) {
	s := caFixture(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	// One body, four versions of the SAME release carrying it. 18.9.0 is the
	// lexical maximum; 18.10.0 is the real one.
	exec(`INSERT INTO paragraphs VALUES (90, 'the UE shall perform registration')`)
	exec(`INSERT INTO bodies (body_id, heading) VALUES (90, 'Registration')`)
	exec(`INSERT INTO body_seq VALUES (90, 1, 90)`) // (body_id, ord, para_id)
	for i, v := range []string{"18.2.0", "18.9.0", "18.10.0", "18.10.1"} {
		exec(`INSERT INTO clause_occ VALUES (?, '38.331', 'Rel-18', ?, '5.3', true, 90)`,
			7000+i, v)
	}

	hits, err := s.SearchClauses(context.Background(), SearchQuery{
		Text: "registration", TopK: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	var got string
	for _, h := range hits {
		if h.Clause.SpecID == "38.331" && h.Clause.ClausePath == "5.3" {
			if got != "" {
				t.Fatalf("the clause came back twice (%s and %s): the collapse is broken", got, h.Clause.Version)
			}
			got = h.Clause.Version
		}
	}
	if got == "" {
		t.Fatal("the clause did not come back at all")
	}
	if got != "18.10.1" {
		t.Errorf("the hit cites v%s; the newest version carrying that text is v18.10.1 "+
			"(a string sort puts 18.9.0 on top — see versionRecencySQL)", got)
	}
}
