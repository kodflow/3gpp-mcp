package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// THE REGRESSION THIS PINS, measured on the shipped corpus 2026-09-05: asked for
// the 30 main 5GC network functions, resolve_term was right about two, wrong
// about nine, silent about nineteen. AMF came back "Authentication Management
// Field", UPF "User Port Function", NEF "Network Element Function".
//
// Every one of those rows is honestly sourced. What was wrong is the ORDER: the
// query said `ORDER BY domain`, domain is empty on essentially every row, so the
// winner was storage order. A caller reads the first row, so an arbitrary first
// row is a wrong answer with a citation attached.
//
// The rank is not a preference invented here — TS 23.501 §3.2 states it, and
// every 3GPP Abbreviations clause opens the same way: "An abbreviation defined
// in the present document takes precedence over the definition of the same
// abbreviation, if any, in TR 21.905 [1]." A row whose source_series names a
// SPEC ("23.501") beats one that names the general vocabulary ("21").
func TestResolveTermRanksTheDefiningSpecFirst(t *testing.T) {
	s := newRankStore(t)
	// Inserted worst-first on purpose: if the ranking is dropped, storage order
	// hands back the legacy meaning and this test fails, which is the point.
	//
	// The defining expansion is also chosen to sort LAST alphabetically of the
	// three. Without that, `ORDER BY ..., expansion` alone would surface
	// "Access and Mobility Management Function" and the test would pass on a
	// build whose precedence clause had been deleted — proving the alphabet,
	// not the rank.
	put(t, s, "AMF", "ATM Mapping Functions", "")
	put(t, s, "AMF", "Authentication Management Field", "21")
	put(t, s, "AMF", "Zone-based Access and Mobility Management Function", "23.501")

	got, err := s.ResolveTerm(context.Background(), "AMF")
	if err != nil {
		t.Fatalf("ResolveTerm: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3 — ranking must not drop meanings: %+v", len(got), got)
	}
	if got[0].Expansion != "Zone-based Access and Mobility Management Function" {
		t.Errorf("first row = %q (source %q), want the 23.501 definition",
			got[0].Expansion, got[0].SourceSeries)
	}
	// source_series was in the schema and in the model, and the query never
	// selected it — so every answer carried an empty provenance while
	// server.go's federation comment claimed a caller could tell the halves
	// apart by it.
	if got[0].SourceSeries != "23.501" {
		t.Errorf("first row source_series = %q, want %q — provenance must reach the caller",
			got[0].SourceSeries, "23.501")
	}
}

// NEGATIVE CONTROL. The ranking must not swallow the meanings it demotes: a term
// really can mean two things, and the tool's job is to show both with the
// defining one first. A "fix" that returned only the 23.501 row would pass the
// test above and lose the corpus's own vocabulary.
func TestResolveTermKeepsTheOtherMeanings(t *testing.T) {
	s := newRankStore(t)
	put(t, s, "AMF", "Access and Mobility Management Function", "23.501")
	put(t, s, "AMF", "Authentication Management Field", "21")

	got, err := s.ResolveTerm(context.Background(), "AMF")
	if err != nil {
		t.Fatalf("ResolveTerm: %v", err)
	}
	var seenLegacy bool
	for _, a := range got {
		if a.Expansion == "Authentication Management Field" {
			seenLegacy = true
		}
	}
	if !seenLegacy {
		t.Error("the 21.905 meaning disappeared; demoting is not deleting")
	}
}

// NEGATIVE CONTROL. A term no spec redefines must still resolve — the ranking
// applies to ties, it is not a filter.
func TestResolveTermStillAnswersWithoutASpecSourcedRow(t *testing.T) {
	s := newRankStore(t)
	put(t, s, "IMSI", "International Mobile Subscriber Identity", "21")

	got, err := s.ResolveTerm(context.Background(), "IMSI")
	if err != nil {
		t.Fatalf("ResolveTerm: %v", err)
	}
	if len(got) != 1 || got[0].Expansion != "International Mobile Subscriber Identity" {
		t.Fatalf("got %+v, want the single 21.905 row", got)
	}
}

// Case-insensitivity is what the tool documents; ranking must not have broken it.
func TestResolveTermIsCaseInsensitive(t *testing.T) {
	s := newRankStore(t)
	put(t, s, "AMF", "Access and Mobility Management Function", "23.501")
	for _, q := range []string{"amf", "AMF", "Amf"} {
		got, err := s.ResolveTerm(context.Background(), q)
		if err != nil {
			t.Fatalf("ResolveTerm(%q): %v", q, err)
		}
		if len(got) != 1 {
			t.Errorf("ResolveTerm(%q) returned %d rows, want 1", q, len(got))
		}
	}
}

// THE SAME DEFECT, STILL LIVE ON THE OTHER HALF — measured on the shipped corpus
// 2026-09-07: 938 of the 3 042 terms in data/etsi.duckdb carry more than one
// expansion, and every one of the 4 941 rows carried the constant source_series
// "etsi".
//
// So the precedence bucket above could not separate them — "etsi" names no
// document — and the tie-break decided the answer: `domain, expansion`, with
// domain empty on every ETSI row, is the ALPHABET. Asked for a term 3GPP does not
// define, the federated resolve_term read back, first:
//
//	MSC  -> "Main Service Channel"      instead of the Mobile Switching Centre
//	IMS  -> "IP Multimdia Subsystem"    a typo, above the correct spelling
//	TS   -> "SimulCrypt involves the …" a sentence, above "Technical Specification"
//
// ETSI publishes no TR 21.905 and states no precedence, so there is no authority
// to rank by — only agreement, counted at write time by ingest-glossary. This
// asserts the count decides, and that it decides AGAINST the alphabet: the winner
// here sorts last of the three.
func TestResolveTermRanksTheETSIConsensusFirst(t *testing.T) {
	s := newRankStore(t)
	putETSI(t, s, "MSC", "Main Service Channel", "ETSI EN 300 175-1", 1)
	putETSI(t, s, "MSC", "Message Sequence Chart", "ETSI ES 201 873-1", 4)
	putETSI(t, s, "MSC", "Mobile-services Switching Centre", "ETSI TS 101 200", 37)

	got, err := s.ResolveTerm(context.Background(), "MSC")
	if err != nil {
		t.Fatalf("ResolveTerm: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3 — ranking must not drop meanings: %+v", len(got), got)
	}
	if got[0].Expansion != "Mobile-services Switching Centre" {
		t.Errorf("first row = %q (declared by %d), want the expansion the archive agrees on",
			got[0].Expansion, got[0].DeclaredBy)
	}
	if got[0].DeclaredBy != 37 {
		t.Errorf("first row declared_by = %d, want 37 — the count must reach the caller, "+
			"or the ranking cannot be checked by the person reading it", got[0].DeclaredBy)
	}
	// PROVENANCE, which "etsi" could never carry: the row now names a deliverable
	// a reader can open.
	if got[0].SourceSeries != "ETSI TS 101 200" {
		t.Errorf("first row source_series = %q, want the declaring deliverable", got[0].SourceSeries)
	}
	// The demoted meanings stay, in count order. Ranking is not filtering.
	if got[1].Expansion != "Message Sequence Chart" {
		t.Errorf("second row = %q, want the next-most-declared meaning", got[1].Expansion)
	}
}

// NEGATIVE CONTROL: the count must not disturb the 3GPP half, which ranks by the
// rule TS 23.501 §3.2 states and counts nothing.
//
// A spec-sourced row carries no count — NULL, read as 1 — so a row with a count
// of 40 must still lose to it. Getting this backwards would trade one arbitrary
// answer for another: "the most repeated expansion" is not the rule 3GPP
// publishes about itself.
func TestTheCountDoesNotOutrankTheDefiningSpec(t *testing.T) {
	s := newRankStore(t)
	putETSI(t, s, "AMF", "ATM Mapping Function", "ETSI TS 102 221", 40)
	put(t, s, "AMF", "Access and Mobility Management Function", "23.501")

	got, err := s.ResolveTerm(context.Background(), "AMF")
	if err != nil {
		t.Fatalf("ResolveTerm: %v", err)
	}
	if got[0].Expansion != "Access and Mobility Management Function" {
		t.Errorf("first row = %q, want the 23.501 definition: agreement ranks a corpus "+
			"that states no precedence, it does not overrule one that does", got[0].Expansion)
	}
}

// An uncounted row must not sink below a counted one for want of a count: NULL
// means "not counted", and every 3GPP writer means exactly one declaration by it.
func TestAnUncountedRowIsWorthOneDeclaration(t *testing.T) {
	s := newRankStore(t)
	put(t, s, "UICC", "Universal Integrated Circuit Card", "")
	putETSI(t, s, "UICC", "Universal Idle Circuit Card", "ETSI TS 102 221", 1)

	got, err := s.ResolveTerm(context.Background(), "UICC")
	if err != nil {
		t.Fatalf("ResolveTerm: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(got), got)
	}
	// Tied at one declaration each, the stable tie-break decides — the alphabet,
	// which is right where nothing better is known and is exactly what it must not
	// be where something is.
	if got[0].Expansion != "Universal Idle Circuit Card" {
		t.Errorf("first row = %q; a tie must fall back to the stable order rather than "+
			"drop the uncounted row to the bottom", got[0].Expansion)
	}
}

func newRankStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// putETSI writes a row shaped the way ingest-glossary writes one: the declaring
// deliverable as provenance, its version in the release fields, and the number of
// deliverables that declare that exact expansion.
func putETSI(t *testing.T, s *Store, term, expansion, deliverable string, declaredBy int) {
	t.Helper()
	if err := s.UpsertAcronym(model.Acronym{
		Term: term, Expansion: expansion, SourceSeries: deliverable,
		FirstRelease: "1.1.1", LastRelease: "1.1.1", DeclaredBy: declaredBy,
	}); err != nil {
		t.Fatalf("upsert %s/%s: %v", term, expansion, err)
	}
}

func put(t *testing.T, s *Store, term, expansion, source string) {
	t.Helper()
	if err := s.UpsertAcronym(model.Acronym{
		Term: term, Expansion: expansion, SourceSeries: source,
		FirstRelease: "Rel-20", LastRelease: "Rel-20",
	}); err != nil {
		t.Fatalf("upsert %s/%s: %v", term, expansion, err)
	}
}

// A CORPUS WITHOUT THE COLUMN MUST STILL ANSWER — and this is not hypothetical:
// it is every image published before declared_by existed.
//
// cmd/server opens the corpus with OpenReadOnly, which opens
// access_mode=read_only and runs no migration by design ("schema already
// exists"). Only Open applies the ALTER. So a binary carrying the new query and
// a corpus predating the column meet in exactly the shape that killed search_api
// for 84 % of the corpus in build 23: a query that names a column which is not
// there does not degrade, it fails, and the tool is dead for every term.
//
// The store probes the capability instead, the way it already probes the
// content-addressed shape, and the older corpus keeps precisely the order it had.
func TestResolveTermAnswersOnACorpusWithoutTheCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.duckdb")

	rw, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	putETSI(t, rw, "MSC", "Main Service Channel", "ETSI EN 300 175-1", 1)
	putETSI(t, rw, "MSC", "Mobile Switching Centre", "ETSI TS 101 200", 9)
	// Make it an OLD corpus: the column goes away, exactly as it is absent from
	// every DB built before this change.
	if _, err := rw.DB().Exec(`ALTER TABLE acronyms DROP COLUMN declared_by`); err != nil {
		t.Fatalf("drop declared_by: %v", err)
	}
	if err := rw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("open read-only: %v", err)
	}
	defer func() { _ = ro.Close() }()
	if ro.declaredBy {
		t.Fatal("the probe reports a column the corpus does not have")
	}

	got, err := ro.ResolveTerm(context.Background(), "MSC")
	if err != nil {
		t.Fatalf("ResolveTerm on a corpus without declared_by: %v — resolve_term would "+
			"be dead for every term on every image published before the column", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(got), got)
	}
	// Unranked by count, the order is the one that corpus always had: the bucket,
	// then the alphabet. Asserting it pins that the fallback is the OLD behaviour
	// and not some third thing.
	if got[0].Expansion != "Main Service Channel" {
		t.Errorf("first row = %q, want the order the old corpus had", got[0].Expansion)
	}
	if got[0].DeclaredBy != 1 {
		t.Errorf("declared_by = %d on a corpus that cannot store it, want 1", got[0].DeclaredBy)
	}
}
