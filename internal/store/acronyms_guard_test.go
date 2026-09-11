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
//   - 29.999 is the real signature: catalogued, owning rows, and not one row from
//     it in the batch. It is named with what it owns and what the write deletes —
//     its SMF row is not deleted, it passes to 23.501, which is why a vanished
//     spec cannot be measured by its deletions alone.
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
