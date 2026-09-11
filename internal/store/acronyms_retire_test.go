package store

import (
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// storing is v with the given rows' keys added to what TS 21.905's writer stores —
// the vocabulary of a corpus whose "21" rows are all current, which is what every
// real corpus is (measured 2026-09-11: the 404 "21" rows are 404 of the 1 300
// keys v19.2.0 stores). A fixture that holds a "21" row its vocabulary does not
// store is a corpus with a STALE TS 21.905 row, and that row is retired.
func storing(v GeneralVocabulary, rows ...model.Acronym) GeneralVocabulary {
	out := GeneralVocabulary{Read: v.Read, Release: v.Release, Pairs: map[GeneralPair]bool{}}
	for k := range v.Pairs {
		out.Pairs[k] = true
	}
	for _, a := range rows {
		out.Pairs[GeneralPair{Term: a.Term, Expansion: a.Expansion}] = true
	}
	return out
}

// TS 21.905 v10.3.0's TTCN row, and v19.2.0's. The corpus holds only the second:
// v10.3.0 printed the first, a later issue corrected it — the churn measured on
// 2026-09-11 over the 16 stored versions (7 of 15 issues dropped 1 to 8 keys).
var (
	ttcnV10 = model.Acronym{Term: "TTCN", Expansion: "Tree and Tabular Combined Notation",
		FirstRelease: "Rel-10", LastRelease: "Rel-10", SourceSeries: "21"}
	ttcnV19 = model.Acronym{Term: "TTCN", Expansion: "TTCN-2 or TTCN-3",
		FirstRelease: "Rel-19", LastRelease: "Rel-19", SourceSeries: "21"}
	newestStoresTTCN = GeneralVocabulary{Read: true, Release: "Rel-19",
		Pairs: map[GeneralPair]bool{{Term: "TTCN", Expansion: "TTCN-2 or TTCN-3"}: true}}
)

// A KEY THE NEWEST TS 21.905 DROPPED DOES NOT OUTLIVE IT.
//
// The Rust ingest writes a new TS 21.905 version's rows into its shard, and the
// fold copies them into the corpus ON CONFLICT DO NOTHING: the corrected TTCN
// arrives, and the old TTCN stays, stamped "21", for good — resolve_term then
// cites TS 21.905 for an expansion TS 21.905 no longer prints. Nothing else ever
// deleted a "21" row. This is the retirement: the old row goes, the current one
// stays, the seeded rows are untouched, and the next identical run writes nothing.
func TestAKeyTheNewestTS21905DroppedIsRetired(t *testing.T) {
	s := openScratch(t)
	if err := s.UpsertAcronym(ttcnV19); err != nil {
		t.Fatal(err)
	}
	// The seeded glossary is already current, so the run below has NOTHING to do
	// but the retirement: a retirement alone must be a change, or the write is
	// skipped as "already correct" and the stale row stays.
	if _, err := s.ReplaceSeededAcronyms([]model.Acronym{amf}, newestStoresTTCN, nil); err != nil {
		t.Fatal(err)
	}
	// Then the stale row arrives, as the fold would leave it.
	if err := s.UpsertAcronym(ttcnV10); err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanSeededAcronyms([]model.Acronym{amf}, newestStoresTTCN)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := s.ReplaceSeededAcronyms([]model.Acronym{amf}, newestStoresTTCN, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Retired) != 1 || diff.Retired[0] != ttcnV10 {
		t.Errorf("diff.Retired = %+v, want the v10.3.0 TTCN row alone", diff.Retired)
	}
	if len(plan.Retired) != len(diff.Retired) {
		t.Errorf("--check-only planned %d retirement(s), the write did %d", len(plan.Retired), len(diff.Retired))
	}
	if !diff.Changed() {
		t.Error("a run that deleted a row reported no change — the corpus would not be re-published")
	}
	got := glossary(t, s)
	if _, still := got[keyOf(ttcnV10)]; still {
		t.Errorf("TTCN = %q survived although the newest TS 21.905 no longer stores it", ttcnV10.Expansion)
	}
	if cur, ok := got[keyOf(ttcnV19)]; !ok || cur != ttcnV19 {
		t.Errorf("the CURRENT TS 21.905 row was touched: %+v (present=%v)", cur, ok)
	}
	if cur, ok := got[keyOf(amf)]; !ok || cur != amf {
		t.Errorf("the seeded row was not written beside the retirement: %+v (present=%v)", cur, ok)
	}
	if again, err := s.ReplaceSeededAcronyms([]model.Acronym{amf}, newestStoresTTCN, nil); err != nil {
		t.Fatal(err)
	} else if again.Changed() || len(again.Retired) != 0 {
		t.Errorf("the run after the retirement reported a change: %+v", again)
	}
}

// AN UNREAD TS 21.905 RETIRES NOTHING. With no evidence of what the newest
// version stores, "not stored" cannot be told from "not read" — the same rule
// that withholds every seeded release.
func TestAnUnreadTS21905RetiresNothing(t *testing.T) {
	s := openScratch(t)
	if err := s.UpsertAcronym(ttcnV10); err != nil {
		t.Fatal(err)
	}
	diff, err := s.ReplaceSeededAcronyms([]model.Acronym{amf}, GeneralVocabulary{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Retired) != 0 {
		t.Errorf("an UNREAD TS 21.905 retired %+v", diff.Retired)
	}
	if _, ok := glossary(t, s)[keyOf(ttcnV10)]; !ok {
		t.Error("a TS 21.905 row was deleted on no evidence")
	}
}

// A STALE "21" KEY A SPEC DECLARES IS NOT RETIRED: the batch takes it over, as it
// takes over any "21" row whose key a spec declares, and the row stays in the
// glossary under the spec that prints it.
func TestAStaleTS21905KeyASpecDeclaresIsTakenOverNotRetired(t *testing.T) {
	s := openScratch(t)
	if err := s.UpsertAcronym(ttcnV10); err != nil {
		t.Fatal(err)
	}
	spec := model.Acronym{Term: ttcnV10.Term, Expansion: ttcnV10.Expansion,
		FirstRelease: "19.0.0", LastRelease: "19.0.0", SourceSeries: "34.123-1", DeclaredBy: 1}
	diff, err := s.ReplaceSeededAcronyms([]model.Acronym{amf, spec}, newestStoresTTCN, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Retired) != 0 {
		t.Errorf("a key a spec declares was retired: %+v", diff.Retired)
	}
	if cur := glossary(t, s)[keyOf(ttcnV10)]; cur != spec {
		t.Errorf("the key was not taken over by the spec that declares it: %+v", cur)
	}
}
