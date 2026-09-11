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

// AN ABBREVIATIONS HEADING WITH NO NUMBER OWNS ONLY ITSELF. Walking on from it
// would read every unnumbered clause to the end of the document — here an
// unrelated one carrying a perfectly acronym-shaped line — and hand its key back
// to TS 21.905 although the writer never stored it. extract_acronyms stops there
// too; the two readers must not disagree about what the region is.
func TestAnUnnumberedAbbreviationsHeadingOwnsOnlyItself(t *testing.T) {
	c := func(chunk uint64, path, heading string) model.Clause {
		return model.Clause{ChunkID: chunk, SpecID: ts21905, Version: "19.2.0", Release: "Rel-19",
			ClausePath: path, Heading: heading}
	}
	for _, root := range []string{"", "Annex B"} {
		got := generalRegion([]model.Clause{
			c(1, root, "Abbreviations"),
			c(2, "", "Unrelated"),
			c(3, "", "Also unrelated"),
		})
		var headings []string
		for _, cl := range got.clauses {
			headings = append(headings, cl.Heading)
		}
		if strings.Join(headings, ",") != "Abbreviations" {
			t.Errorf("an Abbreviations heading at %q took %v — it must own only itself", root, headings)
		}
	}
}

// A HAND-BACK IS NOT A DELETION, AND THE GUARD DOES NOT COUNT IT. 54 taken-over
// rows dropped at once, against a bound of 53, all handed back: every key stays
// in resolve_term, cited as TS 21.905's, and the run passes.
//
// The hand-back as first written refused exactly this — "release 54 (0 removed,
// 54 handed back to TS 21.905, 0 withheld)" — on the ground that each had been a
// removal before the hand-back existed. The guard stops deletions; a run that
// deletes nothing is never refused by it.
func TestTheGuardDoesNotCountHandedBackRows(t *testing.T) {
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
	if err != nil {
		t.Fatalf("54 hand-backs against a bound of 53 were refused, and the run deletes nothing: %v", err)
	}
	if rep.Guard != "pass" || rep.Restored != 54 || rep.Removed != 0 || rep.RemovalBound != 53 {
		t.Errorf("guard=%q restored=%d removed=%d bound=%d, want pass/54/0/53",
			rep.Guard, rep.Restored, rep.Removed, rep.RemovalBound)
	}
	got := terms(t, path)
	for _, k := range []string{"T001", "T054"} {
		if got[k] != "21" {
			t.Errorf("%s is stamped %q after the run; it must be TS 21.905's again (\"21\")", k, got[k])
		}
	}
}

// rowsFor lists a term's glossary rows as "expansion | source", sorted.
func rowsFor(t *testing.T, path, term string) []string {
	t.Helper()
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	rows, err := s.DB().Query(`SELECT expansion || ' | ' || coalesce(source_series, '') FROM acronyms
		WHERE term = ? ORDER BY 1`, term)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// A HAND-BACK RESTORES WHAT TS 21.905'S WRITER STORES, AND INVENTS NOTHING.
//
// The entries are TS 21.905 v19.2.0's, verbatim, and the "21" rows are the ones
// its writer stored for them — both as the local corpus holds them on
// 2026-09-11. Every seeded row below is dropped by the sweep at once:
//
//   - ADM wraps onto a second line. The writer keeps the first and holds a "21"
//     row cut there; the seed joins the wrap, so 51.011 holds the whole
//     expansion under a key the writer never stored.
//   - CFNRc: TS 21.905 prints a double space, and so does its "21" row; the seed
//     collapses it.
//   - "JAR file": the writer's pattern refuses a space in a term, so there is no
//     "21" row at all.
//
// The hand-back as first written handed each of those three back — the seed's
// parser read the same text and found the key — and left ADM and CFNRc with TWO
// rows ResolveTerm serves as TS 21.905's, and JAR file with one its writer never
// wrote. They are removed, like any row a spec dropped.
//
//   - CA: TS 21.905 prints two expansions and the writer stored both;
//     "Carrier Aggregation" was taken over (38.903 holds it, 92 specs declare it)
//     and "Capacity Allocation" is still TS 21.905's. The KEY is decided, not the
//     term: it goes back, beside the "21" row that was never taken.
//   - EF, likewise, and the case that rules out matching against the "21" rows
//     instead of the writer's keys: "Elementary File" reads as a cut of
//     "Elementary File (on the UICC)", and it is a line of its own that the
//     writer stored. mirror swaps which of the two was taken over.
func TestAHandBackRestoresOnlyWhatTS21905sWriterStores(t *testing.T) {
	const (
		admCut  = "Access condition to an EF which is under the control of the"
		admFull = admCut + " authority which creates this file"
		cfnrc21 = "Call Forwarding on mobile subscriber  Not Reachable"
	)
	for _, mirror := range []bool{false, true} {
		t.Run(fmt.Sprintf("mirror=%v", mirror), func(t *testing.T) {
			efTaken, ef21 := "Elementary File", "Elementary File (on the UICC)"
			if mirror {
				efTaken, ef21 = ef21, efTaken
			}
			path := handBackFixture(t, func(s *store.Store) {
				withTS21905(t, s, ts21905Min, "SN\tSerial Number",
					"ADM\t"+admCut+"\nauthority which creates this file",
					"CFNRc\t"+cfnrc21,
					"JAR file\tJava Archive File",
					"CA\tCarrier Aggregation", "CA\tCapacity Allocation",
					"EF\tElementary File (on the UICC)", "EF\tElementary File")
				general := func(term, exp string) model.Acronym {
					return model.Acronym{Term: term, Expansion: exp, FirstRelease: "Rel-19",
						LastRelease: "Rel-19", SourceSeries: "21"}
				}
				taken := func(term, exp, spec string) model.Acronym {
					return model.Acronym{Term: term, Expansion: exp, FirstRelease: "18.0.0",
						LastRelease: "18.0.0", SourceSeries: spec, DeclaredBy: 1}
				}
				for _, a := range []model.Acronym{
					general("ADM", admCut), taken("ADM", admFull, "51.011"),
					general("CFNRc", cfnrc21), taken("CFNRc", "Call Forwarding on mobile subscriber Not Reachable", "23.018"),
					taken("JAR file", "Java Archive File", "23.057"),
					general("CA", "Capacity Allocation"), taken("CA", "Carrier Aggregation", "38.903"),
					general("EF", ef21), taken("EF", efTaken, "31.102"),
				} {
					if err := s.UpsertAcronym(a); err != nil {
						t.Fatal(err)
					}
				}
			})
			opt := Options{Specs: []string{"23.501"}, Min: 1}
			check := opt
			check.CheckOnly = true
			crep, err := Run(context.Background(), path, check)
			if err != nil {
				t.Fatal(err)
			}
			rep, err := Run(context.Background(), path, opt)
			if err != nil {
				t.Fatal(err)
			}
			// SN and OLD are handBackFixture's own: one handed back, one removed.
			if rep.Restored != 3 || rep.Removed != 4 {
				t.Errorf("restored=%d removed=%d, want 3 (SN, CA, EF) and 4 (ADM, CFNRc, JAR file, OLD)\n"+
					"restored: %+v\nremoved: %+v", rep.Restored, rep.Removed, rep.RestoredRows, rep.RemovedRows)
			}
			if crep.Restored != rep.Restored || crep.Removed != rep.Removed {
				t.Errorf("--check-only predicted restored=%d removed=%d, the write did %d and %d",
					crep.Restored, crep.Removed, rep.Restored, rep.Removed)
			}
			for term, want := range map[string][]string{
				"ADM":      {admCut + " | 21"},
				"CFNRc":    {cfnrc21 + " | 21"},
				"JAR file": nil,
				"CA":       {"Capacity Allocation | 21", "Carrier Aggregation | 21"},
				"EF":       {"Elementary File (on the UICC) | 21", "Elementary File | 21"},
			} {
				if got := rowsFor(t, path, term); strings.Join(got, "\n") != strings.Join(want, "\n") {
					t.Errorf("%s holds %q, want exactly what TS 21.905's writer stores: %q", term, got, want)
				}
			}
		})
	}
}
