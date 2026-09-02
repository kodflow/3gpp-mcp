package store

import (
	"context"
	"strings"
	"testing"
)

// etsiCaFixture is an ETSI deliverable: no releases at all — parse_etsi_meta
// stamps the constant "ETSI" on every one — and a history that lives entirely in
// the VERSION column.
//
//	P1 "unchanged across every version"   1.1.1  1.2.1  1.3.1
//	P2 "dropped in 1.3.1"                 1.1.1  1.2.1
//	P3 "new in 1.3.1"                                   1.3.1
func etsiCaFixture(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, v := range []string{"1.1.1", "1.2.1", "1.3.1"} {
		exec(`INSERT INTO spec_versions (spec_id, release, version) VALUES (?, 'ETSI', ?)`, "ETSI TS 102 221", v)
	}
	exec(`INSERT INTO paragraphs VALUES (1, 'unchanged across every version'), (2, 'dropped in 1.3.1'), (3, 'new in 1.3.1')`)
	exec(`INSERT INTO bodies (body_id, heading) VALUES (10, 'READ RECORD'), (11, 'READ RECORD')`)
	exec(`INSERT INTO body_seq VALUES (10, 1, 1), (10, 2, 2), (11, 1, 1), (11, 2, 3)`)
	exec(`INSERT INTO clause_occ VALUES (1, 'ETSI TS 102 221', 'ETSI', '1.1.1', '11.1.5.1', true, 10)`)
	exec(`INSERT INTO clause_occ VALUES (2, 'ETSI TS 102 221', 'ETSI', '1.2.1', '11.1.5.1', true, 10)`)
	exec(`INSERT INTO clause_occ VALUES (3, 'ETSI TS 102 221', 'ETSI', '1.3.1', '11.1.5.1', true, 11)`)
	s.probeContentAddressed(context.Background())
	if !s.ContentAddressed() {
		t.Fatal("the fixture was not detected as content-addressed")
	}
	return s
}

// THE DEFECT. ClauseDelta grouped by o.release, which is the constant "ETSI" on
// every deliverable, so the ETSI half answered every delta with added=nil,
// removed=nil, kept=0. That is not an error the caller can see: it reads as "this
// clause did not change between those two points", on a corpus that keeps every
// version precisely to show that it did.
//
// Falsified: with the axis hard-coded back to `release`, added and removed are
// both empty and kept is 0.
func TestClauseDeltaWorksOnTheVersionAxis(t *testing.T) {
	s := etsiCaFixture(t)
	added, removed, kept, err := s.ClauseDelta(context.Background(), "ETSI TS 102 221", "11.1.5.1", "1.2.1", "1.3.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0] != "new in 1.3.1" {
		t.Errorf("added = %v, want [new in 1.3.1]", added)
	}
	if len(removed) != 1 || removed[0] != "dropped in 1.3.1" {
		t.Errorf("removed = %v, want [dropped in 1.3.1]", removed)
	}
	if kept != 1 {
		t.Errorf("kept = %d, want 1", kept)
	}
}

// An empty diff meant BOTH "nothing changed" and "your endpoints matched
// nothing", and the caller could not tell them apart. Endpoints the spec does not
// hold are now an error that names what it does hold.
func TestClauseDeltaRefusesEndpointsTheSpecDoesNotHold(t *testing.T) {
	s := etsiCaFixture(t)
	_, _, _, err := s.ClauseDelta(context.Background(), "ETSI TS 102 221", "11.1.5.1", "Rel-17", "Rel-18")
	if err == nil {
		t.Fatal("asking an ETSI deliverable for a 3GPP release must fail, not answer an empty diff")
	}
	for _, want := range []string{"Rel-17", "1.1.1", "1.3.1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must name %q so the caller can retry: %v", want, err)
		}
	}
}

// The release axis must keep working exactly as before for a 3GPP spec, and a
// caller who names two VERSIONS of a multi-release spec now gets the answer they
// asked for instead of an empty one.
func TestClauseDeltaStillPrefersReleaseAndAcceptsVersions(t *testing.T) {
	s := caFixture(t)
	added, removed, _, err := s.ClauseDelta(context.Background(), "23.501", "5.2", "Rel-18", "Rel-19")
	if err != nil || len(added) != 1 || len(removed) != 1 {
		t.Fatalf("release axis: added=%v removed=%v err=%v", added, removed, err)
	}
	added, removed, _, err = s.ClauseDelta(context.Background(), "23.501", "5.2", "18.0.0", "19.0.0")
	if err != nil {
		t.Fatalf("two versions of a multi-release spec must resolve on the version axis: %v", err)
	}
	if len(added) != 1 || added[0] != "new in Rel-19" || len(removed) != 1 {
		t.Errorf("version axis on a 3GPP spec: added=%v removed=%v", added, removed)
	}
}
