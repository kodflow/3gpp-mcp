package store

import (
	"errors"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

func catalogue(t *testing.T, s *Store, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := s.UpsertSpec(model.Spec{SpecID: id, Series: id[:2], DocType: "TS"}); err != nil {
			t.Fatal(err)
		}
	}
}

func seeded(term, expansion, spec string) model.Acronym {
	return model.Acronym{Term: term, Expansion: expansion, FirstRelease: "19.0.0",
		LastRelease: "19.0.0", SourceSeries: spec, DeclaredBy: 1}
}

// A SPEC THE SWEEP NO LONGER HEARS FROM IS NAMED — and only that spec.
//
// Vanished is what the mass-removal guard refuses on, so its definition is the
// guard. Three look-alikes must stay out of it, each for a measured reason:
//
//   - 22.999 is read perfectly but ends with no row of its own, because 23.501,
//     read after it, declares the same pair and takes the citation. That is the
//     corpus growing; a guard keyed on "ends with zero rows" would refuse it.
//   - 37.999 is silent too, but the catalogue no longer lists it: the spec left
//     the corpus, and its rows leaving is the replacement doing its job.
//   - 29.999 is the real signature: catalogued, owning rows, not one row from it
//     in the batch, and a row of it — ZZA — deleted. It is named with what it
//     owns and what the write deletes — its SMF row is not deleted, it passes to
//     23.501, which is why a vanished spec cannot be measured by its deletions
//     alone. What it takes for a spec to be named AT ALL is a deletion: see
//     TestASilencedSpecIsNamedOnlyWhenTheWriteWouldDeleteItsRows.
func TestTheDiffNamesACataloguedSpecTheSweepNoLongerHearsFrom(t *testing.T) {
	s := openScratch(t)
	catalogue(t, s, "22.999", "23.501", "29.999")
	for _, a := range []model.Acronym{
		seeded("AMF", "Access and Mobility Management Function", "23.501"),
		seeded("UE", "User Equipment", "22.999"),
		seeded("ZZA", "Zeta Zone Alpha", "29.999"),
		seeded("SMF", "Session Management Function", "29.999"),
		seeded("OLD", "Only Left in Departed spec", "37.999"),
	} {
		if err := s.UpsertAcronym(a); err != nil {
			t.Fatal(err)
		}
	}

	// 22.999 is heard from (UE) but read BEFORE 23.501, which declares UE too.
	batch := []model.Acronym{
		seeded("UE", "User Equipment", "22.999"),
		seeded("AMF", "Access and Mobility Management Function", "23.501"),
		seeded("SMF", "Session Management Function", "23.501"),
		seeded("UE", "User Equipment", "23.501"),
	}
	diff, err := s.PlanSeededAcronyms(batch, cleared)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Owned != 5 {
		t.Errorf("Owned = %d, want the 5 seeded rows the table held before the sweep", diff.Owned)
	}
	want := []VanishedSpec{{Spec: "29.999", Owned: 2, Removed: 1}}
	if len(diff.Vanished) != len(want) || diff.Vanished[0] != want[0] {
		t.Errorf("Vanished = %+v, want %+v — 22.999 (read, citation moved) and 37.999 "+
			"(no longer catalogued) are not the signature of a broken read", diff.Vanished, want)
	}
}

// A REFUSED APPROVAL WRITES NOTHING, and still hands back what it refused.
//
// approve sits between the plan and the transaction; if the refusal came after
// the transaction opened, "nothing was written" would depend on a rollback, and
// if the diff were not returned the operator deciding on --allow-mass-removal
// would be deciding blind.
func TestARefusedApprovalWritesNothing(t *testing.T) {
	s := openScratch(t)
	if _, err := s.ReplaceSeededAcronyms([]model.Acronym{amf, multicastMBS}, cleared, nil); err != nil {
		t.Fatal(err)
	}
	refusal := errors.New("refused by the test")
	diff, err := s.ReplaceSeededAcronyms([]model.Acronym{amf, uaClean}, cleared, func(GlossaryDiff) error { return refusal })
	if !errors.Is(err, refusal) {
		t.Fatalf("the approval's refusal was not returned: %v", err)
	}
	got := glossary(t, s)
	if _, still := got[keyOf(multicastMBS)]; !still {
		t.Error("a REFUSED replacement deleted a row: the refusal came after the write began")
	}
	if _, wrote := got[keyOf(uaClean)]; wrote {
		t.Error("a REFUSED replacement inserted a row: the refusal came after the write began")
	}
	if len(diff.Removed) != 1 || diff.Written != 1 {
		t.Errorf("the refused diff was not handed back (%d removed, %d written): the operator "+
			"cannot see what was refused", len(diff.Removed), diff.Written)
	}
}

// A SILENCED SPEC IS NAMED ONLY WHEN THE WRITE WOULD DELETE ITS ROWS — because
// Vanished is what the mass-removal guard refuses on, and the guard stops
// deletions.
//
// Three specs go silent at once, each catalogued. 29.999 loses a row to the
// delete and is named. The other two lose nothing from resolve_term, and each
// would have refused a run that deletes nothing: 28.999's only row is handed back
// to TS 21.905, 27.999's passes to 23.501, which declares the same pair — and
// when TS 21.905 cannot be read, every dropped row is withheld and NO spec is
// named. The hand-back as first written named 28.999, and every silent spec in
// the unread case; a withheld row never leaves while TS 21.905 stays unread, so
// that refusal came back on every run and --allow-mass-removal could not clear it.
func TestASilencedSpecIsNamedOnlyWhenTheWriteWouldDeleteItsRows(t *testing.T) {
	s := openScratch(t)
	catalogue(t, s, "23.501", "27.999", "28.999", "29.999")
	for _, a := range []model.Acronym{
		seeded("AMF", "Access and Mobility Management Function", "23.501"),
		seeded("ZZA", "Zeta Zone Alpha", "29.999"),
		seeded("SN", "Serial Number", "28.999"),
		seeded("SMF", "Session Management Function", "27.999"),
	} {
		if err := s.UpsertAcronym(a); err != nil {
			t.Fatal(err)
		}
	}
	batch := []model.Acronym{
		seeded("AMF", "Access and Mobility Management Function", "23.501"),
		seeded("SMF", "Session Management Function", "23.501"),
	}

	read, err := s.PlanSeededAcronyms(batch, ts21905)
	if err != nil {
		t.Fatal(err)
	}
	want := []VanishedSpec{{Spec: "29.999", Owned: 1, Removed: 1}}
	if len(read.Vanished) != 1 || read.Vanished[0] != want[0] {
		t.Errorf("TS 21.905 read: Vanished = %+v, want %+v alone — 28.999 (handed back) and 27.999 "+
			"(passed on) lose nothing from resolve_term", read.Vanished, want)
	}
	if len(read.Restored) != 1 || len(read.Removed) != 1 {
		t.Errorf("the premise failed: restored %+v removed %+v, want SN and ZZA", read.Restored, read.Removed)
	}

	unread, err := s.PlanSeededAcronyms(batch, GeneralVocabulary{})
	if err != nil {
		t.Fatal(err)
	}
	if len(unread.Vanished) != 0 {
		t.Errorf("TS 21.905 unread: Vanished = %+v, want none — the write deletes nothing, and a "+
			"withheld row is withheld again by every unread run, so naming its spec refuses them all",
			unread.Vanished)
	}
	if len(unread.Withheld) != 2 || len(unread.Removed) != 0 {
		t.Errorf("the premise failed: withheld %+v removed %+v, want SN and ZZA withheld",
			unread.Withheld, unread.Removed)
	}
}
