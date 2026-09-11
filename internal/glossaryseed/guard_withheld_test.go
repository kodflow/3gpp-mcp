package glossaryseed

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// unreadFixture is a corpus whose TS 21.905 is ABSENT, so every run comes back
// unread and withholds each row the sweep dropped. The sweep yields AMF from
// 23.501; the catalogue lists 23.501 and 29.999.
func unreadFixture(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "glossarywithheld")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "corpus.duckdb")
	withStore(t, path, func(s *store.Store) {
		if err := s.InsertClauses([]model.Clause{{ChunkID: 1, SpecID: "23.501", Release: "Rel-19",
			Version: "19.0.0", ClausePath: "3.2", Heading: "Abbreviations", IsNormative: true,
			Text: "AMF\tAccess and Mobility Management Function"}}); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"23.501", "29.999"} {
			if err := s.UpsertSpec(model.Spec{SpecID: id, Series: id[:2], DocType: "TS"}); err != nil {
				t.Fatal(err)
			}
		}
	})
	return path
}

func withStore(t *testing.T, path string, do func(*store.Store)) {
	t.Helper()
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	do(s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// drop gives the glossary seeded rows S<from>..S<to>, stamped spec, that no
// clause in the corpus declares — what a spec's later issue leaves behind when it
// stops declaring them.
func drop(t *testing.T, path, spec string, from, to int) {
	t.Helper()
	withStore(t, path, func(s *store.Store) {
		for i := from; i <= to; i++ {
			if err := s.UpsertAcronym(model.Acronym{Term: fmt.Sprintf("S%03d", i),
				Expansion: fmt.Sprintf("Stale entry %03d", i), FirstRelease: "18.0.0",
				LastRelease: "18.0.0", SourceSeries: spec, DeclaredBy: 1}); err != nil {
				t.Fatal(err)
			}
		}
	})
}

// WITHHELD ROWS NEVER STOP A RUN — the four runs the hand-back as first written
// got wrong, in order.
//
// No TS 21.905, a bound of 53. A withheld row stays seeded, so the next unread
// run withholds it again and the pile only grows. That version counted it toward
// the bound: run 2 was REFUSED at "release 60 (0 removed, 0 handed back, 60
// withheld)", exit 1, from a run that rewrote nothing; run 3, given
// --allow-mass-removal, removed nothing; run 4 was refused again, and so would
// every enrich have been until TS 21.905 became readable. Before the hand-back
// all four passed, deleting 30 rows twice; now none of them deletes a row, and
// all four must still pass.
//
// THE PROTECTION IS DEFERRED, NOT DROPPED. When TS 21.905 can be read again the
// 60 are judged: TS 21.905's writer stores none of their keys, so all 60 are
// deletions, and THAT run is refused — once — until an operator lets it through.
func TestWithheldRowsNeverTripTheGuard(t *testing.T) {
	path := unreadFixture(t)

	drop(t, path, "23.501", 1, 30)
	rep, err := run(t, path, false, false)
	if err != nil || rep.Guard != "pass" || rep.Withheld != 30 || rep.Removed != 0 {
		t.Fatalf("run 1: guard=%q withheld=%d removed=%d err=%v, want pass/30/0",
			rep.Guard, rep.Withheld, rep.Removed, err)
	}
	if rep.General.Unread == "" {
		t.Fatalf("the premise failed: TS 21.905 was read (%+v)", rep.General)
	}

	drop(t, path, "23.501", 31, 60)
	for i, allow := range []bool{false, true, false} {
		n := i + 2
		rep, err := run(t, path, false, allow)
		if err != nil {
			t.Fatalf("run %d (--allow-mass-removal=%v) was refused, and it deletes nothing: %v", n, allow, err)
		}
		if rep.Guard != "pass" || rep.Withheld != 60 || rep.Removed != 0 || rep.Rewritten != 0 || rep.Changed {
			t.Errorf("run %d: guard=%q withheld=%d removed=%d rewritten=%d changed=%v, want pass/60/0/0/false",
				n, rep.Guard, rep.Withheld, rep.Removed, rep.Rewritten, rep.Changed)
		}
		crep, cerr := run(t, path, true, false)
		if cerr != nil || crep.Guard != "pass" || crep.Withheld != 60 {
			t.Errorf("run %d, --check-only: guard=%q withheld=%d err=%v, want pass/60", n, crep.Guard,
				crep.Withheld, cerr)
		}
	}
	if got := terms(t, path); got["S001"] != "23.501" || got["S060"] != "23.501" {
		t.Errorf("an unread TS 21.905 let rows be released: %v", got)
	}

	// TS 21.905 can be read again, and declares none of the 60.
	withStore(t, path, func(s *store.Store) { withTS21905(t, s, ts21905Min) })
	rep, err = run(t, path, false, false)
	if err == nil || !strings.Contains(err.Error(), "remove 60 of the 61 seeded rows, above the bound of 53") {
		t.Fatalf("the first run to read TS 21.905 deletes 60 rows and was not refused with its numbers: %v", err)
	}
	if rep, err = run(t, path, false, true); err != nil || rep.Guard != "overridden" || rep.Removed != 60 {
		t.Fatalf("--allow-mass-removal: guard=%q removed=%d err=%v, want overridden/60", rep.Guard, rep.Removed, err)
	}
	if rep, err = run(t, path, false, false); err != nil || rep.Guard != "pass" || rep.Removed != 0 {
		t.Errorf("the run after the cleanup: guard=%q removed=%d err=%v, want pass/0", rep.Guard, rep.Removed, err)
	}
}

// A SPEC SILENCED WHILE TS 21.905 IS UNREAD DOES NOT STOP THE RUN — rule 1's half
// of the same defect. 29.999 is catalogued, owns two rows, and the sweep hears
// nothing from it; with TS 21.905 unread both rows are withheld, nothing is
// deleted, and 29.999 is not named. The hand-back as first written named it on
// every run, with or without --allow-mass-removal, for as long as TS 21.905
// stayed unread.
//
// And when TS 21.905 is read again the rows become deletions, and the run that
// would delete them names 29.999 exactly as before.
func TestASpecSilencedWhileTS21905IsUnreadDoesNotStopTheRun(t *testing.T) {
	path := unreadFixture(t)
	drop(t, path, "29.999", 1, 2)

	for i, allow := range []bool{false, true, false} {
		rep, err := run(t, path, false, allow)
		if err != nil || rep.Guard != "pass" || len(rep.Vanished) != 0 || rep.Withheld != 2 {
			t.Fatalf("run %d: guard=%q vanished=%+v withheld=%d err=%v, want pass, none, 2",
				i+1, rep.Guard, rep.Vanished, rep.Withheld, err)
		}
	}

	withStore(t, path, func(s *store.Store) { withTS21905(t, s, ts21905Min) })
	rep, err := run(t, path, false, false)
	if err == nil || !strings.Contains(err.Error(), "29.999 (2 rows)") {
		t.Fatalf("with TS 21.905 read, the run that deletes 29.999's rows did not name it: %v", err)
	}
	if len(rep.Vanished) != 1 || rep.Vanished[0] != (VanishedSpec{Spec: "29.999", Owned: 2, Removed: 2}) {
		t.Errorf("vanished = %+v, want 29.999 owning 2, removing 2", rep.Vanished)
	}
}
