package store

import (
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// cleared is a TS 21.905 that was read and declares none of the fixtures' keys:
// the vocabulary under which every release is a removal, which is what every
// release was before the hand-back existed. The tests written before it pass it
// so that they go on testing exactly what they tested.
var cleared = GeneralVocabulary{Read: true}

// The TS 21.905 entry the hand-back exists for, as the Rust ingest wrote it — and
// as the corpus measured on 2026-09-11 holds it, under 43.048's stamp: 43.048
// declares the same pair, took the key over, and a later issue that drops it
// would have deleted TS 21.905's entry with it.
var (
	sn21905 = model.Acronym{Term: "SN", Expansion: "Serial Number",
		FirstRelease: "Rel-19", LastRelease: "Rel-19", SourceSeries: "21"}
	snTakenOver = model.Acronym{Term: "SN", Expansion: "Serial Number",
		FirstRelease: "18.0.0", LastRelease: "18.0.0", SourceSeries: "43.048", DeclaredBy: 1}
	ts21905 = GeneralVocabulary{Read: true, Release: "Rel-19",
		Pairs: map[GeneralPair]bool{{Term: "SN", Expansion: "Serial Number"}: true}}
)

// declaredByIsNull reads the one thing acronymIndex cannot show: it coalesces
// NULL to 0, and a hand-back that wrote 0 would rank the row below every counted
// one while claiming no document declares it — see declaredBy.
func declaredByIsNull(t *testing.T, s *Store, a model.Acronym) bool {
	t.Helper()
	var null bool
	if err := s.DB().QueryRow(`SELECT declared_by IS NULL FROM acronyms
		WHERE term = ? AND expansion = ? AND domain = ?`, a.Term, a.Expansion, a.Domain).Scan(&null); err != nil {
		t.Fatal(err)
	}
	return null
}

// A TAKEN-OVER ROW TS 21.905 STILL DECLARES GOES BACK TO TS 21.905 — and a row it
// does not declare is still released, in the same call.
//
// The defect (confirmed 2026-09-11): when a spec declares a pair TS 21.905 also
// declares, the seed overwrites the "21" row with the spec's; when the spec later
// drops the pair, the replacement read the row as seeded — its CURRENT stamp is a
// spec id — and deleted it. TS 21.905's entry left resolve_term while TS 21.905
// still declared it, and no step puts it back. 905 rows of the corpus measured
// that day are in exactly that position.
//
// What is checked is the ROW, not only the count: back to exactly what the Rust
// ingest writes for it, declared_by NULL included, so a restored entry cannot be
// told from one that was never taken over.
func TestATakenOverRowTS21905StillDeclaresIsHandedBack(t *testing.T) {
	s := openScratch(t)
	if err := s.UpsertAcronym(sn21905); err != nil {
		t.Fatal(err)
	}
	// The takeover — pre-existing, and correct: a spec's own declaration outranks
	// TS 21.905's (TS 23.501 §3.2).
	if _, err := s.ReplaceSeededAcronyms([]model.Acronym{amf, snTakenOver, multicastMBS}, ts21905, nil); err != nil {
		t.Fatal(err)
	}
	if got := glossary(t, s)[keyOf(sn21905)]; got != snTakenOver {
		t.Fatalf("the premise failed: the spec did not take the key over, the row is %+v", got)
	}

	// 43.048 stops declaring SN; 23.783 stops declaring Multicast, which TS 21.905
	// does not declare — the control, released as before.
	after := []model.Acronym{amf}
	plan, err := s.PlanSeededAcronyms(after, ts21905)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := s.ReplaceSeededAcronyms(after, ts21905, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Restored) != 1 || diff.Restored[0] != snTakenOver {
		t.Errorf("diff.Restored = %+v, want the taken-over SN row as it stood", diff.Restored)
	}
	if len(diff.Removed) != 1 || diff.Removed[0] != multicastMBS {
		t.Errorf("diff.Removed = %+v, want Multicast alone — a row TS 21.905 does not declare "+
			"must still be released", diff.Removed)
	}
	if !diff.Changed() || diff.Released() != 2 {
		t.Errorf("changed=%v released=%d, want true/2", diff.Changed(), diff.Released())
	}
	if len(plan.Restored) != len(diff.Restored) || len(plan.Removed) != len(diff.Removed) {
		t.Errorf("--check-only planned %d hand-back(s) and %d removal(s), the write did %d and %d",
			len(plan.Restored), len(plan.Removed), len(diff.Restored), len(diff.Removed))
	}

	got := glossary(t, s)
	sn, ok := got[keyOf(sn21905)]
	if !ok {
		t.Fatalf("TS 21.905's SN = %q was DELETED when 43.048 stopped declaring it, and TS 21.905 "+
			"still declares it — the entry is gone from resolve_term and no step puts it back",
			sn21905.Expansion)
	}
	if sn != sn21905 {
		t.Errorf("SN was kept but not handed back: %+v, want TS 21.905's row %+v", sn, sn21905)
	}
	if !declaredByIsNull(t, s, sn21905) {
		t.Error("the handed-back row carries a declared_by; TS 21.905's writer stores NULL")
	}
	if _, still := got[keyOf(multicastMBS)]; still {
		t.Error("the control failed: Multicast, which TS 21.905 does not declare, was not released")
	}

	// A HANDED-BACK ROW IS TS 21.905's AGAIN, so the next identical sweep has
	// nothing to do with it: not seeded, not dropped, not written.
	if again, err := s.ReplaceSeededAcronyms(after, ts21905, nil); err != nil {
		t.Fatal(err)
	} else if again.Changed() || again.Released() != 0 {
		t.Errorf("re-running the sweep after the hand-back reported a change: %+v", again)
	}
}

// AN UNREAD TS 21.905 RELEASES NOTHING — no removal, no hand-back — and says which
// rows it held back; what the batch declares is still written.
//
// The zero value is the unread one on purpose: a caller that forgets to read
// TS 21.905 must land here, not on "TS 21.905 declares none of these", which is
// the empty set a failed read would otherwise hand over — and which deletes every
// taken-over entry at once.
func TestAnUnreadTS21905ReleasesNothing(t *testing.T) {
	s := openScratch(t)
	if err := s.UpsertAcronym(sn21905); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceSeededAcronyms([]model.Acronym{amf, snTakenOver, multicastMBS}, ts21905, nil); err != nil {
		t.Fatal(err)
	}

	diff, err := s.ReplaceSeededAcronyms([]model.Acronym{amf, uaClean}, GeneralVocabulary{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Removed) != 0 || len(diff.Restored) != 0 {
		t.Errorf("an UNREAD TS 21.905 still released rows: removed %+v, restored %+v",
			diff.Removed, diff.Restored)
	}
	if len(diff.Withheld) != 2 || diff.Withheld[0] != multicastMBS || diff.Withheld[1] != snTakenOver {
		t.Errorf("diff.Withheld = %+v, want Multicast and SN as they stand", diff.Withheld)
	}
	got := glossary(t, s)
	for _, kept := range []model.Acronym{snTakenOver, multicastMBS} {
		if cur, ok := got[keyOf(kept)]; !ok || cur != kept {
			t.Errorf("%s = %q was touched although TS 21.905 could not be read: now %+v (present=%v)",
				kept.Term, kept.Expansion, cur, ok)
		}
	}
	if cur, ok := got[keyOf(uaClean)]; !ok || cur != uaClean {
		t.Errorf("withholding the releases also withheld the batch: UA is %+v (present=%v)", cur, ok)
	}
	// Withheld rows are what the table already holds: nothing to write for them.
	if again, err := s.ReplaceSeededAcronyms([]model.Acronym{amf, uaClean}, GeneralVocabulary{}, nil); err != nil {
		t.Fatal(err)
	} else if again.Changed() || len(again.Withheld) != 2 {
		t.Errorf("a re-run with TS 21.905 still unread reported changed=%v withheld=%d, want false/2",
			again.Changed(), len(again.Withheld))
	}
}

// THE HAND-BACK LEAVES A FILE THE NEXT RUN DOES NOT MOVE — the byte-level half of
// "an identical re-run writes nothing", for the path this change adds. A restore
// that the next plan saw as different again (a release stamp, a declared_by, a
// domain that did not round-trip) would rewrite the row on every run, and every
// run would push the corpus.
func TestAnIdenticalSweepAfterAHandBackLeavesTheFileByteIdentical(t *testing.T) {
	path, cleanup := scratchDB(t)
	defer cleanup()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAcronym(sn21905); err != nil {
		t.Fatal(err)
	}
	for _, batch := range [][]model.Acronym{{amf, snTakenOver}, {amf}} {
		if _, err := s.ReplaceSeededAcronyms(batch, ts21905, nil); err != nil {
			t.Fatal(err)
		}
	}
	// Present is the premise; WHAT it was handed back as is the test above. A
	// hand-back stamped with anything seededBySpec accepts — "21.905" reads like a
	// tidier stamp than "21" — is exactly what would never converge: every sweep
	// would see the row as seeded and dropped, and hand it back again.
	if _, ok := glossary(t, s)[keyOf(sn21905)]; !ok {
		t.Fatal("the premise failed: SN was not handed back, it is gone")
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
	diff, err := s.ReplaceSeededAcronyms([]model.Acronym{amf}, ts21905, nil)
	if err == nil {
		_, err = s.DB().Exec(`CHECKPOINT`)
	}
	if cerr := s.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	if diff.Changed() {
		t.Errorf("the sweep after a hand-back reported a change: %+v", diff)
	}
	if after := sha(t, path); after != before {
		t.Errorf("the sweep after a hand-back MOVED the file (%s -> %s)", before[:16], after[:16])
	}
}
