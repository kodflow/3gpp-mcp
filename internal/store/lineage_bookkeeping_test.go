package store

import (
	"context"
	"path/filepath"
	"testing"
)

// A CATALOGUE ROW WITH NO DOCUMENT BEHIND IT IS NOT A REMOVAL.
//
// The lineage axis comes from spec_versions, which lists the releases the 3GPP
// catalogue FILES a spec under — and 3GPP routinely files a spec under a release
// it has not re-issued it for, at the previous release's version. On the corpus
// published 2026-09-12, before any repair, 8 such filings carry no occurrence at
// all, and trace_clause(29.675, 4) answered `last_seen=Rel-19 obsolete=true` for
// a clause nobody removed: 29.675 is filed under Rel-20 at its Rel-19 version and
// no Rel-20 document exists.
//
// Obsolescence is measured against the newest release the corpus holds TEXT for.
func TestABookkeepingFilingIsNotEvidenceOfRemoval(t *testing.T) {
	ctx := context.Background()
	s := lineageFixture(t, func(exec func(string, ...any)) {
		// Filed under three releases; text under two. Rel-20 is the catalogue's
		// bookkeeping row, at the Rel-19 version, with no document behind it.
		exec(`INSERT INTO spec_versions (spec_id, release, version) VALUES
			('29.675','Rel-18','18.4.0'), ('29.675','Rel-19','19.2.0'), ('29.675','Rel-20','19.2.0')`)
		exec(`INSERT INTO clauses (chunk_id, spec_id, release, version, clause_path, heading, text, is_normative) VALUES
			(1,'29.675','Rel-18','18.4.0','4','H','t',true),
			(2,'29.675','Rel-19','19.2.0','4','H','t',true),
			(3,'29.675','Rel-18','18.4.0','5','H','t',true)`)
	})
	defer func() { _ = s.Close() }()

	lin, err := s.ClauseLineage(ctx, "29.675", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := lin["4"]; got.Obsolete {
		t.Errorf("clause 4 is in the newest release the corpus has text for (Rel-19): obsolete must be false, got %+v", got)
	}
	// The one that really did stop: present only in Rel-18, gone by Rel-19.
	if got := lin["5"]; !got.Obsolete {
		t.Errorf("clause 5 is absent from Rel-19, which HAS text: obsolete must be true, got %+v", got)
	}
	// The axis is untouched — what the catalogue files is still reported.
	_, ordered, err := s.ClauseAvailability(ctx, "29.675", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ordered) != 3 || ordered[2] != "Rel-20" {
		t.Errorf("the axis must still be the catalogue's three releases, got %v", ordered)
	}
}

// THE YARDSTICK IS THE SPEC'S, NOT THE SUBTREE'S. trace_clause filters by prefix;
// taking the filtered rows' own newest release would make every traced clause
// non-obsolete — the same defect facing the other way.
func TestObsolescenceIsMeasuredAgainstTheWholeSpec(t *testing.T) {
	ctx := context.Background()
	s := lineageFixture(t, func(exec func(string, ...any)) {
		exec(`INSERT INTO spec_versions (spec_id, release, version) VALUES
			('23.501','Rel-18','18.0.0'), ('23.501','Rel-19','19.0.0')`)
		exec(`INSERT INTO clauses (chunk_id, spec_id, release, version, clause_path, heading, text, is_normative) VALUES
			(1,'23.501','Rel-18','18.0.0','6.1','H','t',true),
			(2,'23.501','Rel-19','19.0.0','7.1','H','t',true)`)
	})
	defer func() { _ = s.Close() }()

	lin, err := s.ClauseLineage(ctx, "23.501", "6")
	if err != nil {
		t.Fatal(err)
	}
	if got := lin["6.1"]; !got.Obsolete {
		t.Errorf("6.1 exists only in Rel-18 while the spec has Rel-19 text: obsolete must be true even when the trace is filtered to 6.x, got %+v", got)
	}
}

// lineageFixture builds a tiny unconverted corpus and runs seed against it.
func lineageFixture(t *testing.T, seed func(exec func(string, ...any))) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "c.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	seed(func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(context.Background(), q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	})
	return s
}
