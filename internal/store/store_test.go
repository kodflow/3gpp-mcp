package store

import (
	"context"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

func newMem(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func seedSpec(t *testing.T, s *Store) {
	t.Helper()
	if err := s.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS", WorkingGroup: "SA3"}); err != nil {
		t.Fatal(err)
	}
	for _, vv := range []struct{ rel, ver string }{{"Rel-17", "17.20.0"}, {"Rel-18", "18.15.0"}, {"Rel-19", "19.6.0"}} {
		if err := s.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: vv.rel, Version: vv.ver}); err != nil {
			t.Fatal(err)
		}
	}
	clauses := []model.Clause{
		{ChunkID: 1, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.2.2", Heading: "LI at AMF", Text: "amf interception"},
		{ChunkID: 2, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.2.2.2", Heading: "Generation of xIRI over LI_X2", Text: "x2 to mdf2"},
		{ChunkID: 3, SpecID: "33.128", Release: "Rel-19", Version: "19.6.0", ClausePath: "6.2.2.2.2", Heading: "Registration", Text: "registration event"},
	}
	if err := s.InsertClauses(clauses); err != nil {
		t.Fatal(err)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newMem(t)
	seedSpec(t, s)

	if n, _ := s.CountClauses(ctx); n != 3 {
		t.Errorf("clauses = %d, want 3", n)
	}

	// clause subtree fetch
	cs, err := s.GetClauses(ctx, "33.128", "19.6.0", "6.2.2.2")
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 { // 6.2.2.2 and 6.2.2.2.2
		t.Errorf("subtree 6.2.2.2 = %d clauses, want 2", len(cs))
	}

	// LIKE fallback search (FTS not enabled on :memory: offline)
	hits, err := s.SearchClauses(ctx, SearchQuery{Text: "registration", Filter: SpecFilter{SpecID: "33.128"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Citation.SpecID != "33.128" {
		t.Errorf("LIKE search returned %d hits (want >=1 with citation)", len(hits))
	}

	// release ordering: newest first
	vs, err := s.ListReleases(ctx, "33.128")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 3 || vs[0].Version != "19.6.0" {
		t.Errorf("ListReleases order wrong: %+v", vs)
	}
	if _, v, ok, _ := s.LatestVersion(ctx, "33.128"); !ok || v != "19.6.0" {
		t.Errorf("LatestVersion = %q ok=%v, want 19.6.0", v, ok)
	}
}

func TestAcronymMultiExpansion(t *testing.T) {
	ctx := context.Background()
	s := newMem(t)
	for _, exp := range []string{"Access and Mobility Management Function", "Authentication Management Field"} {
		if err := s.UpsertAcronym(model.Acronym{Term: "AMF", Expansion: exp}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ResolveTerm(ctx, "amf") // case-insensitive
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("AMF expansions = %d, want 2 (both kept)", len(got))
	}
}

func TestEvolutions(t *testing.T) {
	ctx := context.Background()
	s := newMem(t)
	evos := []model.Evolution{
		{FromTerm: "MME", ToTerm: "AMF", EvolutionType: "SPLIT", JustificationSpec: "23.501", Confidence: 0.9},
		{FromTerm: "MME", ToTerm: "SMF", EvolutionType: "SPLIT", JustificationSpec: "23.501", Confidence: 0.75},
	}
	if err := s.InsertEvolutions(evos); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetEvolutions(ctx, "mme") // case-insensitive
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("MME evolutions = %d, want 2", len(got))
	}
}
