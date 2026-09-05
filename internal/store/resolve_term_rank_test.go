package store

import (
	"context"
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
	put(t, s, "AMF", "ATM Mapping Functions", "")
	put(t, s, "AMF", "Authentication Management Field", "21")
	put(t, s, "AMF", "Access and Mobility Management Function", "23.501")

	got, err := s.ResolveTerm(context.Background(), "AMF")
	if err != nil {
		t.Fatalf("ResolveTerm: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3 — ranking must not drop meanings: %+v", len(got), got)
	}
	if got[0].Expansion != "Access and Mobility Management Function" {
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

func newRankStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
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
