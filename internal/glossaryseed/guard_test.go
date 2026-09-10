package glossaryseed

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// guardFixture builds a corpus whose sweep yields AMF and SMF from 23.501, and a
// glossary that already holds `stale` seeded rows 23.501 no longer declares —
// plus, when silent is set, two rows owned by 29.999, which the catalogue lists
// and whose Abbreviations clause the corpus does not hold: a spec the sweep lost.
func guardFixture(t *testing.T, stale int, silent bool) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "glossaryguard")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "corpus.duckdb")

	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := s.InsertClauses([]model.Clause{{
		ChunkID: 1, SpecID: "23.501", Release: "Rel-19", Version: "19.0.0",
		ClausePath: "3.2", Heading: "Abbreviations", IsNormative: true,
		Text: "AMF\tAccess and Mobility Management Function\nSMF\tSession Management Function",
	}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"23.501", "29.999"} {
		if err := s.UpsertSpec(model.Spec{SpecID: id, Series: id[:2], DocType: "TS"}); err != nil {
			t.Fatal(err)
		}
	}
	rows := []model.Acronym{{Term: "3GPP", Expansion: "Third Generation Partnership Project",
		FirstRelease: "Rel-19", LastRelease: "Rel-19", SourceSeries: "21"}}
	for i := 1; i <= stale; i++ {
		rows = append(rows, model.Acronym{Term: fmt.Sprintf("S%03d", i),
			Expansion: fmt.Sprintf("Stale entry %03d", i), FirstRelease: "18.0.0",
			LastRelease: "18.0.0", SourceSeries: "23.501", DeclaredBy: 1})
	}
	if silent {
		rows = append(rows,
			model.Acronym{Term: "ZZA", Expansion: "Zeta Zone Alpha", FirstRelease: "19.0.0",
				LastRelease: "19.0.0", SourceSeries: "29.999", DeclaredBy: 1},
			model.Acronym{Term: "ZZB", Expansion: "Zeta Zone Beta", FirstRelease: "19.0.0",
				LastRelease: "19.0.0", SourceSeries: "29.999", DeclaredBy: 1})
	}
	for _, a := range rows {
		if err := s.UpsertAcronym(a); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func run(t *testing.T, path string, checkOnly, allow bool) (Report, error) {
	t.Helper()
	return Run(context.Background(), path, Options{Specs: []string{"23.501"}, Min: 1,
		CheckOnly: checkOnly, AllowMassRemoval: allow})
}

// A SWEEP THAT SILENCES A CATALOGUED SPEC IS REFUSED — before a row is written,
// and by --check-only in exactly the same words.
//
// This is the failure the replacement introduced: the floor watches the six
// preferred specs, so a read that loses 29.999 passes it, and a replacing write
// then deletes everything 29.999 declared. The additive writer before it could
// only have left those rows stale.
func TestTheGuardRefusesASweepThatSilencesACataloguedSpec(t *testing.T) {
	path := guardFixture(t, 0, true)

	rep, err := run(t, path, false, false)
	if err == nil || !strings.Contains(err.Error(), "29.999 (2 rows)") ||
		!strings.Contains(err.Error(), "lose ALL their rows") {
		t.Fatalf("a sweep that lost 29.999 was not refused by name: err=%v", err)
	}
	if rep.Guard != "refused" || rep.Applied || rep.OK {
		t.Errorf("the refusal is misreported: guard=%q applied=%v ok=%v", rep.Guard, rep.Applied, rep.OK)
	}
	got := terms(t, path)
	if got["ZZA"] != "29.999" || got["ZZB"] != "29.999" {
		t.Errorf("a REFUSED run deleted the silenced spec's rows anyway: %v", got)
	}
	if _, wrote := got["AMF"]; wrote {
		t.Errorf("a REFUSED run still wrote the sweep's rows: %v", got)
	}

	// --check-only: the same verdict, the same message, and still nothing written.
	crep, cerr := run(t, path, true, false)
	if cerr == nil || cerr.Error() != err.Error() {
		t.Errorf("--check-only reached a different verdict than the write:\n write: %v\n check: %v", err, cerr)
	}
	if crep.Guard != "refused" || len(crep.Vanished) != 1 || crep.Vanished[0] != (VanishedSpec{"29.999", 2, 2}) {
		t.Errorf("--check-only did not report the silenced spec: guard=%q vanished=%+v", crep.Guard, crep.Vanished)
	}
	if got := terms(t, path); got["ZZA"] != "29.999" {
		t.Errorf("--check-only wrote: %v", got)
	}
}

// ONE STALE ROW IS ORDINARY, and the guard must let it through: it is exactly
// what the shipped corpus's next run looks like (1 removal of 13 722, measured
// 2026-09-11). A guard that refused it would stop every enrich.
func TestTheGuardLetsAnOrdinaryRemovalThrough(t *testing.T) {
	path := guardFixture(t, 1, false)
	rep, err := run(t, path, false, false)
	if err != nil {
		t.Fatalf("an ordinary one-row removal was refused: %v", err)
	}
	if rep.Guard != "pass" || rep.Removed != 1 || !rep.Changed {
		t.Errorf("guard=%q removed=%d changed=%v, want pass/1/true", rep.Guard, rep.Removed, rep.Changed)
	}
	if _, still := terms(t, path)["S001"]; still {
		t.Error("the stale row survived a run the guard passed")
	}
}

// THE BOUND IS EXACT AT ITS EDGE: removing removalBound rows passes, one more is
// refused and writes nothing. On a small glossary the bound is the absolute floor
// (53, the measured p99 of rows one spec owns), which is what these fixtures hit.
func TestTheRemovalBoundIsExactAtItsEdge(t *testing.T) {
	if b := removalBound(53); b != 53 {
		t.Fatalf("removalBound(53) = %d; this test is written against the 53-row floor", b)
	}

	at := guardFixture(t, 53, false)
	if rep, err := run(t, at, false, false); err != nil || rep.Guard != "pass" || rep.Removed != 53 {
		t.Errorf("removing exactly the bound (53) was refused or misreported: guard=%q removed=%d err=%v",
			rep.Guard, rep.Removed, err)
	}

	over := guardFixture(t, 54, false)
	rep, err := run(t, over, false, false)
	if err == nil || !strings.Contains(err.Error(), "remove 54 of the 54 seeded rows, above the bound of 53") {
		t.Fatalf("removing 54 rows against a bound of 53 was not refused with its numbers: %v", err)
	}
	if rep.Guard != "refused" {
		t.Errorf("guard = %q, want refused", rep.Guard)
	}
	if _, still := terms(t, over)["S054"]; !still {
		t.Error("a REFUSED mass removal deleted rows anyway")
	}
}

// THE OPT-OUT LETS A DELIBERATE CLEANUP THROUGH — the silenced spec and the
// over-bound volume together — and says it did.
func TestTheOptOutLetsADeliberateCleanupThrough(t *testing.T) {
	path := guardFixture(t, 54, true)
	rep, err := run(t, path, false, true)
	if err != nil {
		t.Fatalf("--allow-mass-removal did not let the cleanup through: %v", err)
	}
	if rep.Guard != "overridden" || rep.Removed != 56 || !rep.Changed {
		t.Errorf("guard=%q removed=%d changed=%v, want overridden/56/true", rep.Guard, rep.Removed, rep.Changed)
	}
	got := terms(t, path)
	for _, gone := range []string{"S001", "S054", "ZZA", "ZZB"} {
		if _, still := got[gone]; still {
			t.Errorf("%s survived a cleanup the operator allowed", gone)
		}
	}
	if got["3GPP"] != "21" || got["AMF"] != "23.501" {
		t.Errorf("the cleanup touched what it does not own, or did not write the sweep: %v", got)
	}
}

// The numbers the bound is derived from, pinned: a change to them is a change to
// the policy and should have to say so here.
func TestRemovalBoundFollowsItsDerivation(t *testing.T) {
	for _, c := range []struct{ owned, want int }{
		{13722, 137}, // the shipped corpus, 2026-09-11: 1 % of the seeded rows
		{5400, 54},   // above 5 300 rows the fraction governs
		{5300, 53},   // at and below it, the p99 of one spec's vocabulary does
		{0, 53},
	} {
		if got := removalBound(c.owned); got != c.want {
			t.Errorf("removalBound(%d) = %d, want %d", c.owned, got, c.want)
		}
	}
}
