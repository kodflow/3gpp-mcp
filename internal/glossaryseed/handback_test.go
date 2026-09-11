package glossaryseed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// withTS21905 gives a fixture corpus the TS 21.905 every real corpus holds, in the
// shape measured on all 16 stored versions: "4 Abbreviations" with an empty body,
// the entries in UNNUMBERED letter clauses, then "5 Equations". It declares
// `declares` (tab-separated lines) plus `filler` generated pairs — enough, by
// default, to clear ts21905Min, because a fixture below the floor tests the floor.
//
// Clause 5 carries a tabbed line of its own: a region that ran past its end
// would read "ZZ9 = Zone Zeta Nine" as TS 21.905's.
func withTS21905(t *testing.T, s *store.Store, filler int, declares ...string) {
	t.Helper()
	var fill []string
	for i := 1; i <= filler; i++ {
		fill = append(fill, fmt.Sprintf("G%04d\tGeneral filler entry %04d", i, i))
	}
	clause := func(chunk uint64, path, heading, text string) model.Clause {
		return model.Clause{ChunkID: chunk, SpecID: ts21905, Release: "Rel-19", Version: "19.2.0",
			ClausePath: path, Heading: heading, Text: text, IsNormative: true}
	}
	if err := s.InsertClauses([]model.Clause{
		clause(5000, "3", "Terms and definitions", ""),
		clause(5001, "4", "Abbreviations", ""),
		clause(5002, "", "G", strings.Join(fill, "\n\n")),
		clause(5003, "", "S", strings.Join(declares, "\n\n")),
		clause(5004, "5", "Equations", "ZZ9\tZone Zeta Nine"),
	}); err != nil {
		t.Fatal(err)
	}
}

// handBackFixture is the defect, as a corpus. TS 21.905 declares "SN = Serial
// Number"; 43.048 declared it too and took the key over — the glossary holds it
// under 43.048's stamp, as the corpus measured on 2026-09-11 does — and 43.048's
// current clause no longer declares it. OLD is a seeded row nothing declares, the
// control that must still be removed. build decides what TS 21.905 looks like.
func handBackFixture(t *testing.T, build func(*store.Store)) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "glossaryhandback")
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
	if err := s.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-19", Version: "19.0.0", ClausePath: "3.2",
			Heading: "Abbreviations", IsNormative: true,
			Text: "AMF\tAccess and Mobility Management Function\nSMF\tSession Management Function"},
		{ChunkID: 2, SpecID: "43.048", Release: "Rel-19", Version: "19.0.0", ClausePath: "3.2",
			Heading: "Abbreviations", IsNormative: true, Text: "SS\tSupplementary Service"},
	}); err != nil {
		t.Fatal(err)
	}
	build(s)
	for _, a := range []model.Acronym{
		{Term: "SN", Expansion: "Serial Number", FirstRelease: "18.0.0", LastRelease: "18.0.0",
			SourceSeries: "43.048", DeclaredBy: 1},
		{Term: "OLD", Expansion: "Once Declared, Now Nowhere", FirstRelease: "18.0.0",
			LastRelease: "18.0.0", SourceSeries: "23.501", DeclaredBy: 1},
	} {
		if err := s.UpsertAcronym(a); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// THE RUN HANDS A TAKEN-OVER ROW BACK TO TS 21.905, predicts it on --check-only,
// still removes the row TS 21.905 does not declare — and the run after it leaves
// the corpus byte for byte where it was.
//
// This is the defect end to end: before the hand-back, the second half of this
// test found SN deleted — TS 21.905's entry gone from resolve_term although
// TS 21.905 still declares it — and every gate green, because one row is far
// inside the mass-removal bound and 43.048 is still heard from.
func TestRunHandsATakenOverRowBackToTS21905(t *testing.T) {
	path := handBackFixture(t, func(s *store.Store) {
		withTS21905(t, s, ts21905Min, "SN\tSerial Number")
	})
	opt := Options{Specs: []string{"23.501"}, Min: 1}

	check := opt
	check.CheckOnly = true
	crep, err := Run(context.Background(), path, check)
	if err != nil {
		t.Fatal(err)
	}
	if crep.Restored != 1 || crep.Removed != 1 || crep.General.Unread != "" {
		t.Errorf("--check-only predicted restored=%d removed=%d (TS 21.905 unread: %q), want 1/1",
			crep.Restored, crep.Removed, crep.General.Unread)
	}
	if got := terms(t, path)["SN"]; got != "43.048" {
		t.Errorf("--check-only wrote: SN is now stamped %q", got)
	}

	rep, err := Run(context.Background(), path, opt)
	if err != nil {
		t.Fatal(err)
	}
	got := terms(t, path)
	switch src, ok := got["SN"]; {
	case !ok:
		t.Fatalf("SN = Serial Number was DELETED when 43.048 stopped declaring it, although " +
			"TS 21.905 still declares it — the release took TS 21.905's own entry")
	case src != "21":
		t.Errorf("SN survived but is stamped %q; it must be handed back to TS 21.905 (\"21\")", src)
	}
	if want := (RemovedRow{Term: "SN", Expansion: "Serial Number", Source: "43.048"}); rep.Restored != 1 ||
		len(rep.RestoredRows) != 1 || rep.RestoredRows[0] != want {
		t.Errorf("the report lists restored=%d %+v, want the one row %+v", rep.Restored, rep.RestoredRows, want)
	}
	if _, still := got["OLD"]; still || rep.Removed != 1 {
		t.Errorf("the control failed: OLD, which TS 21.905 does not declare, present=%v removed=%d",
			still, rep.Removed)
	}
	if !rep.Changed || rep.Guard != "pass" || rep.General.Pairs < ts21905Min {
		t.Errorf("changed=%v guard=%q ts21905=%+v", rep.Changed, rep.Guard, rep.General)
	}
	if rep.General.Release != "Rel-19" || rep.General.Version != "19.2.0" {
		t.Errorf("TS 21.905 was read at %q (%q), want 19.2.0 (Rel-19)", rep.General.Version, rep.General.Release)
	}

	// THE CONTRACT "corpus untouched": the run after a hand-back writes nothing.
	before := fileSHA(t, path)
	again, err := Run(context.Background(), path, opt)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Applied || again.Changed || again.Rewritten != 0 || again.Removed != 0 || again.Restored != 0 {
		t.Errorf("the run after the hand-back reported applied=%v changed=%v rewritten=%d removed=%d "+
			"restored=%d, want an untouched corpus", again.Applied, again.Changed, again.Rewritten,
			again.Removed, again.Restored)
	}
	if after := fileSHA(t, path); after != before {
		t.Errorf("the run after the hand-back MOVED the corpus (%s -> %s): a new image layer for "+
			"a glossary that did not change", before[:16], after[:16])
	}
}

// A TS 21.905 THE RUN CANNOT READ RELEASES NOTHING — whichever way the read
// fails — and the run still writes what the sweep declares and says why.
//
// Each failure is the shape a real one would take: the spec missing from a
// partial corpus, a converter that lost the heading, a parse that found the region
// and read almost nothing. An empty set from any of them, handed to the store as
// "read", would say TS 21.905 declares none of the taken-over keys and delete
// every one.
func TestRunReleasesNothingWhenTS21905CannotBeRead(t *testing.T) {
	for _, c := range []struct {
		name, why string
		build     func(*testing.T, *store.Store)
	}{
		{"absent", "not in this corpus", func(*testing.T, *store.Store) {}},
		{"no Abbreviations heading", "no clause headed \"Abbreviations\" in v19.2.0",
			func(t *testing.T, s *store.Store) {
				if err := s.InsertClauses([]model.Clause{{ChunkID: 5001, SpecID: ts21905,
					Release: "Rel-19", Version: "19.2.0", ClausePath: "4", Heading: "Acronyms",
					Text: "SN\tSerial Number", IsNormative: true}}); err != nil {
					t.Fatal(err)
				}
			}},
		{"below the floor", fmt.Sprintf("yields %d pairs, below the floor of %d", ts21905Min-1, ts21905Min),
			func(t *testing.T, s *store.Store) { withTS21905(t, s, ts21905Min-2, "SN\tSerial Number") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := handBackFixture(t, func(s *store.Store) { c.build(t, s) })
			rep, err := Run(context.Background(), path, Options{Specs: []string{"23.501"}, Min: 1})
			if err != nil {
				t.Fatalf("an unreadable TS 21.905 failed the run: %v — it must withhold, not stop enrich", err)
			}
			if !strings.Contains(rep.General.Unread, c.why) {
				t.Errorf("TS 21.905 reported unread=%q, want it to say %q", rep.General.Unread, c.why)
			}
			if rep.Removed != 0 || rep.Restored != 0 || rep.Withheld != 2 {
				t.Errorf("removed=%d restored=%d withheld=%d, want 0/0/2 — a read that failed "+
					"cannot clear any key", rep.Removed, rep.Restored, rep.Withheld)
			}
			got := terms(t, path)
			if got["SN"] != "43.048" || got["OLD"] != "23.501" {
				t.Errorf("a run that could not read TS 21.905 released rows anyway: %v", got)
			}
			if got["AMF"] != "23.501" {
				t.Errorf("withholding the releases also withheld the sweep's own rows: %v", got)
			}
		})
	}
}

// THE REGION IS TS 21.905's LETTER CLAUSES, in document order, at the newest
// version — and nothing after the next numbered clause.
//
// The clauses are handed over in the order GetClauses can return them — the
// letter clauses all have clause_path "", so `len(clause_path), clause_path`
// leaves them tied and physical order decides — with an older version mixed in.
func TestGeneralRegionIsTheLetterClausesOfTheNewestVersion(t *testing.T) {
	c := func(chunk uint64, version, path, heading string) model.Clause {
		return model.Clause{ChunkID: chunk, SpecID: ts21905, Version: version, Release: "Rel-x",
			ClausePath: path, Heading: heading}
	}
	got := generalRegion([]model.Clause{
		c(17, "19.2.0", "", "B"),
		c(11, "19.2.0", "", "Foreword"),
		c(19, "19.2.0", "5", "Equations"),
		c(3, "18.0.0", "4", "Abbreviations"),
		c(4, "18.0.0", "", "A"),
		c(18, "19.2.0", "", "Z"),
		c(15, "19.2.0", "4", "Abbreviations"),
		c(20, "19.2.0", "", "Annex text"),
		c(16, "19.2.0", "", "A"),
		c(12, "19.2.0", "3", "Terms and definitions"),
	})
	var headings []string
	for _, cl := range got.clauses {
		headings = append(headings, cl.Heading)
	}
	if got.version != "19.2.0" || strings.Join(headings, ",") != "Abbreviations,A,B,Z" {
		t.Errorf("region = v%s %v, want v19.2.0 [Abbreviations A B Z] — the letter clauses of the "+
			"newest version in chunk order, stopping at clause 5", got.version, headings)
	}
}

// RULE 2 OF THE GUARD COUNTS A HAND-BACK AS A RELEASE, so its verdict on a sweep
// is what it was before the hand-back existed. 54 taken-over rows dropped at once
// against a bound of 53 is a refusal whether TS 21.905 happens to declare their
// keys or not — the sweep lost them either way — and nothing is written.
func TestTheGuardCountsHandedBackRowsAsReleased(t *testing.T) {
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
	if err := s.InsertClauses([]model.Clause{{ChunkID: 1, SpecID: "23.501", Release: "Rel-19",
		Version: "19.0.0", ClausePath: "3.2", Heading: "Abbreviations", IsNormative: true,
		Text: "AMF\tAccess and Mobility Management Function"}}); err != nil {
		t.Fatal(err)
	}
	var declares []string
	for i := 1; i <= 54; i++ {
		declares = append(declares, fmt.Sprintf("T%03d\tTaken over %03d", i, i))
		if err := s.UpsertAcronym(model.Acronym{Term: fmt.Sprintf("T%03d", i),
			Expansion: fmt.Sprintf("Taken over %03d", i), FirstRelease: "18.0.0", LastRelease: "18.0.0",
			SourceSeries: "23.501", DeclaredBy: 1}); err != nil {
			t.Fatal(err)
		}
	}
	withTS21905(t, s, ts21905Min, declares...)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	rep, err := run(t, path, false, false)
	want := "release 54 (0 removed, 54 handed back to TS 21.905, 0 withheld) of the 54 seeded rows, " +
		"above the bound of 53"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("54 hand-backs against a bound of 53 were not refused with their numbers: %v", err)
	}
	if rep.Guard != "refused" || rep.Restored != 54 {
		t.Errorf("guard=%q restored=%d, want refused/54", rep.Guard, rep.Restored)
	}
	if got := terms(t, path)["T054"]; got != "23.501" {
		t.Errorf("a REFUSED run handed rows back anyway: T054 is stamped %q", got)
	}
}
