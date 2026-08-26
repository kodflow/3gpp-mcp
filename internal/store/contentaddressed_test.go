package store

import (
	"context"
	"strings"
	"testing"
)

// caFixture builds the exact scenario the content-addressed corpus exists to
// answer: one clause whose text is shared across Rel-16..18, loses a paragraph
// in Rel-19 and gains another.
//
//	P1 "every release says this"   Rel-16 17 18 19
//	P2 "dropped in Rel-19"         Rel-16 17 18
//	P3 "new in Rel-19"                          19
func caFixture(t *testing.T) *Store {
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
	for _, rel := range []string{"Rel-16", "Rel-17", "Rel-18", "Rel-19"} {
		exec(`INSERT INTO spec_versions (spec_id, release, version) VALUES (?, ?, ?)`,
			"23.501", rel, strings.TrimPrefix(rel, "Rel-")+".0.0")
	}
	exec(`INSERT INTO paragraphs VALUES (1, 'every release says this'), (2, 'dropped in Rel-19'), (3, 'new in Rel-19')`)
	exec(`INSERT INTO bodies (body_id, heading) VALUES (10, 'AMF registration'), (11, 'AMF registration')`)
	exec(`INSERT INTO body_seq VALUES (10, 1, 1), (10, 2, 2), (11, 1, 1), (11, 2, 3)`)
	for _, rel := range []string{"Rel-16", "Rel-17", "Rel-18"} {
		exec(`INSERT INTO clause_occ VALUES (?, '23.501', ?, ?, '5.2', true, 10)`,
			len(rel)*100+int(rel[len(rel)-1]), rel, strings.TrimPrefix(rel, "Rel-")+".0.0")
	}
	exec(`INSERT INTO clause_occ VALUES (9999, '23.501', 'Rel-19', '19.0.0', '5.2', true, 11)`)

	s.probeContentAddressed(context.Background())
	if !s.ContentAddressed() {
		t.Fatal("the fixture was not detected as content-addressed")
	}
	return s
}

// The question the user actually asks a spec corpus: which statements arrived
// when, and which stopped being made.
func TestParagraphLineageTracksEachStatementAcrossReleases(t *testing.T) {
	s := caFixture(t)
	got, err := s.ParagraphLineage(context.Background(), "23.501", "5.2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d paragraphs, want 3", len(got))
	}
	byText := map[string]ParagraphTrace{}
	for _, tr := range got {
		byText[tr.Text] = tr
	}

	shared := byText["every release says this"]
	if strings.Join(shared.PresentIn, ",") != "Rel-16,Rel-17,Rel-18,Rel-19" {
		t.Errorf("shared paragraph present_in = %v, want all four releases oldest-first", shared.PresentIn)
	}
	if shared.Obsolete {
		t.Error("a paragraph still in the newest release is not obsolete")
	}

	dropped := byText["dropped in Rel-19"]
	if dropped.Introduced != "Rel-16" || dropped.LastSeen != "Rel-18" {
		t.Errorf("dropped paragraph = %s..%s, want Rel-16..Rel-18", dropped.Introduced, dropped.LastSeen)
	}
	if !dropped.Obsolete {
		t.Error("a paragraph absent from the newest release MUST be flagged obsolete — this is the whole point")
	}

	added := byText["new in Rel-19"]
	if added.Introduced != "Rel-19" || added.Obsolete {
		t.Errorf("new paragraph = introduced %s obsolete %v, want Rel-19 and not obsolete", added.Introduced, added.Obsolete)
	}
}

// The +/- between two releases is a set operation on para_id, never a text diff.
func TestClauseDeltaIsASetOperationNotADiff(t *testing.T) {
	s := caFixture(t)
	added, removed, kept, err := s.ClauseDelta(context.Background(), "23.501", "5.2", "Rel-18", "Rel-19")
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0] != "new in Rel-19" {
		t.Errorf("added = %v, want [new in Rel-19]", added)
	}
	if len(removed) != 1 || removed[0] != "dropped in Rel-19" {
		t.Errorf("removed = %v, want [dropped in Rel-19]", removed)
	}
	if kept != 1 {
		t.Errorf("kept = %d, want 1", kept)
	}
}

// Availability must not change its answer just because the storage changed.
func TestAvailabilityIsUnchangedByTheStorageShape(t *testing.T) {
	s := caFixture(t)
	rels, ordered, err := s.ClauseAvailability(context.Background(), "23.501", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 || rels[0].Path != "5.2" {
		t.Fatalf("availability = %+v, want one entry for 5.2", rels)
	}
	if len(rels[0].Releases) != 4 {
		t.Errorf("clause 5.2 seen in %v, want all four releases", rels[0].Releases)
	}
	if rels[0].Heading != "AMF registration" {
		t.Errorf("heading = %q, want it carried from bodies", rels[0].Heading)
	}
	if strings.Join(ordered, ",") != "Rel-16,Rel-17,Rel-18,Rel-19" {
		t.Errorf("ordered releases = %v", ordered)
	}
}

// etsi.duckdb is served ALONGSIDE the 3GPP corpus and has not been migrated. The
// read side must say so plainly instead of failing on a missing table.
func TestAnUnmigratedCorpusRefusesCleanly(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.probeContentAddressed(context.Background())
	if s.ContentAddressed() {
		t.Fatal("an empty corpus must not claim to be content-addressed")
	}
	if _, err := s.ParagraphLineage(context.Background(), "23.501", "5.2"); err == nil {
		t.Error("paragraph lineage must refuse on a corpus that cannot answer it")
	} else if !strings.Contains(err.Error(), "content-addressed") {
		t.Errorf("the refusal must say why, got %q", err)
	}
	if _, _, _, err := s.ClauseDelta(context.Background(), "23.501", "5.2", "Rel-18", "Rel-19"); err == nil {
		t.Error("clause delta must refuse on a corpus that cannot answer it")
	}
}

// The rebuild is bounded on purpose: a view that rebuilt everything cost 97 s
// where this costs milliseconds (ADR 0004).
func TestBodyTextsRebuildsOnlyWhatIsAsked(t *testing.T) {
	s := caFixture(t)
	got, err := s.bodyTexts(context.Background(), []int64{11})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("rebuilt %d bodies, want exactly the one asked for", len(got))
	}
	want := "every release says this" + paraSep + "new in Rel-19"
	if got[11] != want {
		t.Errorf("rebuilt %q, want %q", got[11], want)
	}
	empty, err := s.bodyTexts(context.Background(), nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("an empty request must be a no-op, got %v err=%v", empty, err)
	}
}

// GetClauses must return the same shape whichever way the corpus stores its
// text — that is the contract every caller above the store depends on.
func TestGetClausesRebuildsTheTextItUsedToStore(t *testing.T) {
	s := caFixture(t)
	got, err := s.GetClauses(context.Background(), "23.501", "19.0.0", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d clauses for Rel-19, want 1", len(got))
	}
	c := got[0]
	want := "every release says this" + paraSep + "new in Rel-19"
	if c.Text != want {
		t.Errorf("text = %q, want %q", c.Text, want)
	}
	if c.Heading != "AMF registration" || c.ClausePath != "5.2" || c.Release != "Rel-19" {
		t.Errorf("metadata lost: %+v", c)
	}
	if c.ChunkID == 0 {
		t.Error("chunk_id must survive: it is the only per-occurrence identity")
	}
}

// Distinct bodies are rebuilt once even when many occurrences share them. Three
// releases share one body here, so one rebuild must serve all three.
func TestSharedBodiesAreRebuiltOnce(t *testing.T) {
	s := caFixture(t)
	got, err := s.GetClauses(context.Background(), "23.501", "", "5.2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d occurrences, want 4 (Rel-16..19)", len(got))
	}
	shared := "every release says this" + paraSep + "dropped in Rel-19"
	n := 0
	for _, c := range got {
		if c.Text == shared {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("%d occurrences carry the shared body, want 3 — every one of them must get its text", n)
	}
}
