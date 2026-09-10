package glossaryseed

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// A corpus with one preferred spec declaring two abbreviations, a glossary that
// already holds a seeded row no spec declares any more ("OLD"), and a TS 21.905
// row the seed must never touch. Two entries is far below DefaultMin, which is
// the point: the same corpus fails the floor or passes it depending only on the
// floor the caller asks for.
func replaceFixture(t *testing.T) string {
	t.Helper()
	// Not t.TempDir(): DuckDB can hold the file for a moment after Close() on
	// Windows, and TempDir's cleanup turns that into a failure unrelated to what
	// is being tested (the reason internal/store's scratchDB exists).
	dir, err := os.MkdirTemp("", "glossaryseed")
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
	for _, a := range []model.Acronym{
		{Term: "OLD", Expansion: "Once Declared, Now Nowhere", FirstRelease: "18.0.0",
			LastRelease: "18.0.0", SourceSeries: "23.501", DeclaredBy: 1},
		{Term: "3GPP", Expansion: "Third Generation Partnership Project", FirstRelease: "Rel-19",
			LastRelease: "Rel-19", SourceSeries: "21"},
	} {
		if err := s.UpsertAcronym(a); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func terms(t *testing.T, path string) map[string]string {
	t.Helper()
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	rows, err := s.DB().Query(`SELECT term, coalesce(source_series, '') FROM acronyms`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var term, src string
		if err := rows.Scan(&term, &src); err != nil {
			t.Fatal(err)
		}
		out[term] = src
	}
	return out
}

// (d) A RUN THAT FAILS ITS FLOOR DELETES NOTHING.
//
// The write is now a REPLACEMENT: whatever the batch does not declare, it
// removes. So a broken read that reached the store would not merely leave a few
// rows behind, it would delete every seeded row the read missed — and the floor is
// the only thing that tells a broken read from a smaller vocabulary. This pins
// that nothing below the floor is ever handed to the store.
//
// The positive control is the same corpus with a floor it passes: OLD must then go.
// Without it, a fixture in which OLD could never be removed would make the first
// half pass for the wrong reason.
func TestARunBelowTheFloorRemovesNothing(t *testing.T) {
	path := replaceFixture(t)

	rep, err := Run(context.Background(), path, Options{Specs: []string{"23.501"}, Min: DefaultMin})
	if err == nil {
		t.Fatalf("two abbreviations passed a floor of %d; the premise of this test is wrong", DefaultMin)
	}
	if rep.Applied || rep.Removed != 0 {
		t.Errorf("a run that failed its floor reports applied=%v removed=%d", rep.Applied, rep.Removed)
	}
	got := terms(t, path)
	if got["OLD"] != "23.501" {
		t.Errorf("a run that FAILED ITS FLOOR deleted the seeded row OLD — a broken read now "+
			"costs the glossary every row it missed. Glossary after the run: %v", got)
	}
	if _, wrote := got["AMF"]; wrote {
		t.Errorf("a run that failed its floor still wrote AMF — the write happened before the check")
	}

	// --check-only with a floor it passes: reports the removal, performs none.
	rep, err = Run(context.Background(), path, Options{Specs: []string{"23.501"}, Min: 1, CheckOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Removed != 1 || len(rep.RemovedRows) != 1 || rep.RemovedRows[0].Term != "OLD" {
		t.Errorf("--check-only should predict exactly OLD's removal, reported %d: %+v", rep.Removed, rep.RemovedRows)
	}
	if terms(t, path)["OLD"] == "" {
		t.Error("--check-only DELETED a row; it must write nothing")
	}

	// THE CONTROL: the same corpus, a floor it passes, a real write.
	rep, err = Run(context.Background(), path, Options{Specs: []string{"23.501"}, Min: 1})
	if err != nil {
		t.Fatal(err)
	}
	got = terms(t, path)
	if _, still := got["OLD"]; still || rep.Removed != 1 || !rep.Changed {
		t.Errorf("the control failed: OLD present=%v, removed=%d, changed=%v — this fixture cannot "+
			"show a removal, so the first half proved nothing", still, rep.Removed, rep.Changed)
	}
	if got["3GPP"] != "21" {
		t.Errorf("the TS 21.905 row was touched by the seed: %v", got)
	}
	if got["AMF"] != "23.501" || got["SMF"] != "23.501" {
		t.Errorf("the sweep's own rows were not written: %v", got)
	}
}
