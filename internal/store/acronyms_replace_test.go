package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// The two rows the first sweep under ReplaceSeededAcronyms measured on the
// shipped corpus (2026-09-11): one the miner stopped producing when #323 changed
// which 23.783 clause it mines, and one it still produces because internal/abbrev
// glues change-request markers onto the entry above them. The fixtures use them
// verbatim so the tests describe the corpus rather than an invented one.
var (
	multicastMBS = model.Acronym{Term: "Multicast", Expansion: "MBS session",
		FirstRelease: "18.0.0", LastRelease: "18.0.0", SourceSeries: "23.783", DeclaredBy: 1}
	uaWithCRMarkers = model.Acronym{Term: "UA",
		Expansion:    "User Agent ***** END SET OF CHANGES ***** ***** BEGIN SET OF CHANGES *****",
		FirstRelease: "0.2.0", LastRelease: "0.2.0", SourceSeries: "33.802", DeclaredBy: 1}
	uaClean = model.Acronym{Term: "UA", Expansion: "User Agent",
		FirstRelease: "19.1.0", LastRelease: "19.1.0", SourceSeries: "33.790", DeclaredBy: 24}
	amf = model.Acronym{Term: "AMF", Expansion: "Access and Mobility Management Function",
		FirstRelease: "20.2.0", LastRelease: "20.2.0", SourceSeries: "23.501", DeclaredBy: 74}
	multiPartSpec = model.Acronym{Term: "UE", Expansion: "User Equipment",
		FirstRelease: "19.0.0", LastRelease: "19.0.0", SourceSeries: "38.101-1", DeclaredBy: 3}
)

func openScratch(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "c.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func glossary(t *testing.T, s *Store) map[acronymKey]model.Acronym {
	t.Helper()
	rows, err := s.acronymIndex()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func keyOf(a model.Acronym) acronymKey { return acronymKey{a.Term, a.Expansion, a.Domain} }

// (a) A SEEDED ROW NO SPEC DECLARES ANY MORE LEAVES THE GLOSSARY.
//
// The defect: the glossary write only inserted and updated, so a row the miner
// stopped producing stayed for ever at the highest precedence resolve_term has.
// "Multicast = MBS session" is the one the shipped corpus carries; the corrupt UA
// row stands for the day internal/abbrev stops producing it, which is the day
// this replacement is what lets the fix reach the corpus at all.
func TestASeededRowNoSpecDeclaresAnyMoreIsRemoved(t *testing.T) {
	s := openScratch(t)
	before := []model.Acronym{amf, uaClean, uaWithCRMarkers, multicastMBS, multiPartSpec}
	if _, err := s.ReplaceSeededAcronyms(before, cleared, nil); err != nil {
		t.Fatal(err)
	}

	after := []model.Acronym{amf, uaClean, multiPartSpec}

	// The check-only view of the same sweep must predict exactly the removal the
	// write then performs — and write nothing while doing it.
	plan, err := s.PlanSeededAcronyms(after, cleared)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(glossary(t, s)); got != len(before) {
		t.Fatalf("PlanSeededAcronyms changed the table: %d rows, want %d", got, len(before))
	}

	diff, err := s.ReplaceSeededAcronyms(after, cleared, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Changed() {
		t.Error("a sweep that drops two seeded rows reported no change — the corpus would not be pushed")
	}
	var removed []string
	for _, a := range diff.Removed {
		removed = append(removed, a.Term+"="+a.Expansion)
	}
	want := []string{"Multicast=MBS session", "UA=" + uaWithCRMarkers.Expansion}
	if strings.Join(removed, "|") != strings.Join(want, "|") {
		t.Errorf("diff.Removed = %q, want %q (sorted by term, expansion)", removed, want)
	}
	if len(plan.Removed) != len(diff.Removed) || plan.Written != diff.Written {
		t.Errorf("the check-only plan (%d removed, %d written) disagrees with the write "+
			"(%d removed, %d written) — --check-only would misreport the next run",
			len(plan.Removed), plan.Written, len(diff.Removed), diff.Written)
	}

	got := glossary(t, s)
	for _, gone := range []model.Acronym{multicastMBS, uaWithCRMarkers} {
		if _, still := got[keyOf(gone)]; still {
			t.Errorf("%s = %q (%s) survived a sweep that no longer declares it — "+
				"the glossary still only grows", gone.Term, gone.Expansion, gone.SourceSeries)
		}
	}
	for _, kept := range after {
		if a, ok := got[keyOf(kept)]; !ok || a != kept {
			t.Errorf("%s = %q was declared by the sweep and is now %+v (present=%v)",
				kept.Term, kept.Expansion, a, ok)
		}
	}
	if len(got) != len(after) {
		t.Errorf("%d rows after the replacement, want %d", len(got), len(after))
	}

	// And the replacement converges: the same sweep again is a no-op.
	if again, err := s.ReplaceSeededAcronyms(after, cleared, nil); err != nil {
		t.Fatal(err)
	} else if again.Changed() {
		t.Errorf("re-running the sweep that removed the rows reported a change: %+v", again)
	}
}

// (b) A ROW THE SEED DOES NOT OWN IS NEVER TOUCHED, whatever the sweep says.
//
// Every provenance another writer stamps is here — TS 21.905's series, an ETSI
// deliverable, the legacy ETSI constant, and the NULL a pre-PR-4 corpus carries
// — beside one seeded row the sweep drops, so the test also proves the
// replacement DID run its deletion in the same call. Without that control, a
// replacement that deleted nothing at all would pass.
func TestRowsTheSeedDoesNotOwnSurviveTheReplacement(t *testing.T) {
	s := openScratch(t)
	foreign := []model.Acronym{
		{Term: "AMF", Expansion: "ATM Mapping Function", FirstRelease: "Rel-19",
			LastRelease: "Rel-19", SourceSeries: "21"},
		{Term: "LI", Expansion: "Lawful Interception", FirstRelease: "3.1.1",
			LastRelease: "3.1.1", SourceSeries: "ETSI TS 103 221-1", DeclaredBy: 40},
		{Term: "MSC", Expansion: "Mobile Switching Centre", FirstRelease: "ETSI",
			LastRelease: "ETSI", SourceSeries: "etsi"},
	}
	for _, a := range foreign {
		if err := s.UpsertAcronym(a); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB().Exec(`INSERT INTO acronyms
		(term, expansion, domain, first_release, last_release, source_series, declared_by)
		VALUES ('PLMN', 'Public Land Mobile Network', '', 'Rel-8', 'Rel-8', NULL, NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceSeededAcronyms([]model.Acronym{amf, multicastMBS}, cleared, nil); err != nil {
		t.Fatal(err)
	}

	diff, err := s.ReplaceSeededAcronyms([]model.Acronym{amf}, cleared, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := glossary(t, s)
	for _, a := range foreign {
		if cur, ok := got[keyOf(a)]; !ok || cur != a {
			t.Errorf("a row the seed does not own (source %q) was deleted or rewritten: now %+v (present=%v)",
				a.SourceSeries, cur, ok)
		}
	}
	if _, ok := got[acronymKey{"PLMN", "Public Land Mobile Network", ""}]; !ok {
		t.Error("a row with NULL provenance was deleted — NULL is nobody's, least of all the seed's")
	}
	// THE CONTROL: the seeded row the sweep dropped did go, in this same call.
	if _, still := got[keyOf(multicastMBS)]; still || len(diff.Removed) != 1 {
		t.Errorf("the control failed: the dropped seeded row survived (present=%v, removed=%d), "+
			"so this test proved nothing about ownership", still, len(diff.Removed))
	}
}

// (c) AN IDENTICAL SWEEP WRITES NOTHING — not a row, not a byte.
//
// The contract cmd/seed-glossary prints as "already correct, corpus untouched",
// and it is load-bearing: one changed byte in the 23 GB corpus is a new image
// layer and an 11 GB push. A replacement that deleted and re-inserted rows it
// already held would honour "the rows are right" and break "the file did not
// move", so the FILE is what is compared — the same measure
// TestNoOpWriteLeavesTheFileByteIdentical takes, for the same reason.
func TestAnIdenticalSweepLeavesTheFileByteIdentical(t *testing.T) {
	path, cleanup := scratchDB(t)
	defer cleanup()
	sweep := []model.Acronym{amf, uaClean, uaWithCRMarkers, multicastMBS, multiPartSpec}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAcronym(model.Acronym{Term: "3GPP", Expansion: "Third Generation Partnership Project",
		FirstRelease: "Rel-19", LastRelease: "Rel-19", SourceSeries: "21"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceSeededAcronyms(sweep, cleared, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`CHECKPOINT`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before := sha(t, path)

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := s.ReplaceSeededAcronyms(sweep, cleared, nil)
	if err != nil {
		_ = s.Close()
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`CHECKPOINT`); err != nil {
		_ = s.Close()
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if diff.Changed() {
		t.Errorf("an identical sweep reported a change: %d written, %d removed", diff.Written, len(diff.Removed))
	}
	if after := sha(t, path); after != before {
		t.Errorf("an identical sweep MOVED the file (%s -> %s): the image layer takes a new digest "+
			"and the corpus is pushed again for a glossary that did not change", before[:16], after[:16])
	}
}

// AN EMPTY BATCH IS NOT A SWEEP. The only way to produce one is a read that found
// nothing, and a caller that turned the floor off must not be able to turn that
// into a glossary with no spec-declared row left in it.
func TestAnEmptyBatchRemovesNothing(t *testing.T) {
	s := openScratch(t)
	if _, err := s.ReplaceSeededAcronyms([]model.Acronym{amf, multicastMBS}, cleared, nil); err != nil {
		t.Fatal(err)
	}
	diff, err := s.ReplaceSeededAcronyms(nil, cleared, nil)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Changed() || len(glossary(t, s)) != 2 {
		t.Errorf("an empty batch was read as \"every spec stopped declaring its vocabulary\": "+
			"%d removed, %d rows left of 2", len(diff.Removed), len(glossary(t, s)))
	}
}

// A ROW THE REPLACEMENT COULD NEVER REMOVE IS REFUSED, and refusing it writes
// nothing — not the refused row, and not the valid rows beside it.
func TestABatchRowWithForeignProvenanceIsRefused(t *testing.T) {
	s := openScratch(t)
	_, err := s.ReplaceSeededAcronyms([]model.Acronym{amf,
		{Term: "EIR", Expansion: "Equipment Identity Centre", SourceSeries: "21"}}, cleared, nil)
	if err == nil || !strings.Contains(err.Error(), "not a spec id") {
		t.Fatalf("a batch row stamped %q was accepted (err=%v): it would outlive every later sweep",
			"21", err)
	}
	if n := len(glossary(t, s)); n != 0 {
		t.Errorf("a refused batch still wrote %d row(s)", n)
	}
}
