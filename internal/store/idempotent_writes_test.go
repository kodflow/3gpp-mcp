package store

import (
	"context"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// HNSWFrozenAndCurrent decides whether freeze-hnsw may leave the corpus alone.
// Every test here checks a REFUSAL, and that is deliberate: a false negative
// costs a rebuild, a false positive ships an index that is stale, absent or built
// to other parameters — and nothing downstream reads it, so the corpus would
// serve wrong neighbours with no error anywhere.
func TestHNSWFrozenAndCurrentRefusesEverythingUnproven(t *testing.T) {
	for _, tc := range []struct {
		name string
		meta map[string]string
		want string // a fragment of the reason it must give
	}{
		{
			name: "no index has ever been built",
			meta: map[string]string{},
			want: "not frozen",
		},
		{
			name: "a build was interrupted",
			meta: map[string]string{"hnsw_state": "building"},
			want: "not frozen",
		},
		{
			// The case COPY FROM DATABASE creates: the flag travels with the rows,
			// the index does not. HNSWIndexPresent exists for exactly this.
			name: "schema_meta claims frozen but the index is not there",
			meta: map[string]string{"hnsw_state": "frozen", "embedding_model": "38067f8c6efe"},
			want: "not in the corpus",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, cleanup := scratchDB(t)
			defer cleanup()
			s, err := Open(path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer func() { _ = s.Close() }()
			for k, v := range tc.meta {
				if err := s.SetMeta(k, v); err != nil {
					t.Fatalf("set %s: %v", k, err)
				}
			}
			ok, why := s.HNSWFrozenAndCurrent(context.Background(), "38067f8c6efe")
			if ok {
				t.Fatalf("it approved leaving the corpus alone; it must not")
			}
			if !strings.Contains(why, tc.want) {
				t.Errorf("reason = %q, want something containing %q", why, tc.want)
			}
		})
	}
}

// Re-seeding the same edge set must write nothing and SAY it wrote nothing.
//
// ReplaceEvolutions deletes and re-inserts, so before this it churned the table
// on every enrich. Measured 2026-09-05 on the shipped 23 GB corpus: 524 288 bytes
// per run. The corpus is one layer of the published image and imgtar zeroes tar
// mtimes, so content alone decides the layer digest — half a megabyte of
// difference is an 11 GB upload.
func TestReplaceEvolutionsSkipsAnIdenticalSeed(t *testing.T) {
	path, cleanup := scratchDB(t)
	defer cleanup()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	seed := []model.Evolution{
		{FromTerm: "MME", ToTerm: "AMF", EvolutionType: "SPLIT",
			JustificationSpec: "23.501", JustificationClause: "6.2.1", Confidence: 0.9},
		{FromTerm: "PCRF", ToTerm: "PCF", EvolutionType: "RENAME",
			JustificationSpec: "23.501", JustificationClause: "6.2.4", Confidence: 1},
	}

	changed, err := s.ReplaceEvolutions(ctx, seed)
	if err != nil {
		t.Fatalf("first seed: %v", err)
	}
	if !changed {
		t.Fatal("the first seed wrote nothing; the premise of this test is wrong")
	}

	// THE CASE THAT COST THE PUSH.
	changed, err = s.ReplaceEvolutions(ctx, seed)
	if err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	if changed {
		t.Error("re-applying an identical seed reported a write")
	}

	// Order is not identity — the table has no ordering guarantee and the seed is
	// a set, so a reversed slice is the same seed.
	reversed := []model.Evolution{seed[1], seed[0]}
	if changed, err = s.ReplaceEvolutions(ctx, reversed); err != nil {
		t.Fatalf("reversed: %v", err)
	} else if changed {
		t.Error("the same edges in another order were treated as a change")
	}

	// NEGATIVE CONTROLS. A guard that never writes is the worse failure, because
	// the seed would silently stop tracking its source.
	for _, tc := range []struct {
		name string
		next []model.Evolution
	}{
		{"an edge added", append(append([]model.Evolution{}, seed...),
			model.Evolution{FromTerm: "SGW", ToTerm: "UPF", EvolutionType: "REPLACED_BY",
				JustificationSpec: "23.501", JustificationClause: "6.2.3", Confidence: 0.8})},
		{"an edge removed", seed[:1]},
		{"a citation corrected", []model.Evolution{
			{FromTerm: "MME", ToTerm: "AMF", EvolutionType: "SPLIT",
				JustificationSpec: "23.501", JustificationClause: "6.2.99", Confidence: 0.9},
			seed[1]}},
		{"a confidence changed", []model.Evolution{
			{FromTerm: "MME", ToTerm: "AMF", EvolutionType: "SPLIT",
				JustificationSpec: "23.501", JustificationClause: "6.2.1", Confidence: 0.5},
			seed[1]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed, err := s.ReplaceEvolutions(ctx, tc.next)
			if err != nil {
				t.Fatalf("write: %v", err)
			}
			if !changed {
				t.Errorf("%s was not written", tc.name)
			}
			// Put the baseline back for the next subtest.
			if _, err := s.ReplaceEvolutions(ctx, seed); err != nil {
				t.Fatalf("restore: %v", err)
			}
		})
	}
}

// Re-seeding a glossary that is already correct must write nothing.
//
// THE BUG THIS EXISTS FOR was in the batch, not the database: the identity is
// (term, expansion, domain), and two specs declare the same abbreviation with the
// same wording — 33.501 and 23.401 both write "UP / User Plane". Those arrive as
// two rows sharing one key and differing only in provenance, so each overwrote
// the other within a single call. Comparing each incoming row against the stored
// one then found 145 of 679 "different" on a corpus that was already correct, and
// the 23 GB file moved by ±3.9 MB on every run.
func TestUpsertAcronymsSkipsRowsAlreadyCorrect(t *testing.T) {
	path, cleanup := scratchDB(t)
	defer cleanup()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	// The duplicate-key pair is the point: same term, same expansion, two specs.
	batch := []model.Acronym{
		{Term: "AMF", Expansion: "Access and Mobility Management Function",
			FirstRelease: "20.2.0", LastRelease: "20.2.0", SourceSeries: "23.501"},
		{Term: "UP", Expansion: "User Plane",
			FirstRelease: "20.0.0", LastRelease: "20.0.0", SourceSeries: "23.401"},
		{Term: "UP", Expansion: "User Plane",
			FirstRelease: "20.2.0", LastRelease: "20.2.0", SourceSeries: "33.501"},
	}

	if changed, err := s.UpsertAcronyms(batch); err != nil {
		t.Fatalf("first seed: %v", err)
	} else if !changed {
		t.Fatal("the first seed wrote nothing; the premise of this test is wrong")
	}
	// The table holds one row per (term, expansion, domain): the duplicate pair
	// collapses, and the LAST occurrence is what survives.
	got, err := s.ResolveTerm(context.Background(), "UP")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("UP resolved to %d rows, want 1: %+v", len(got), got)
	}
	if got[0].SourceSeries != "33.501" {
		t.Errorf("UP kept %q; the last occurrence in the batch must win", got[0].SourceSeries)
	}

	// Re-seeding must now be a no-op. Before the fix this wrote the duplicate pair
	// back and forth forever.
	// THE ASSERTION THAT MATTERS. A row COUNT does not move even when the
	// duplicate pair is written back and forth, so counting rows would have
	// passed on the very bug this exists for.
	countBefore := rowCount(t, s, "acronyms")
	if changed, err := s.UpsertAcronyms(batch); err != nil {
		t.Fatalf("re-seed: %v", err)
	} else if changed {
		t.Error("re-seeding an identical glossary reported a write")
	}
	if n := rowCount(t, s, "acronyms"); n != countBefore {
		t.Errorf("re-seeding changed the row count %d -> %d", countBefore, n)
	}

	// NEGATIVE CONTROL: a real change still lands.
	if changed, err := s.UpsertAcronyms([]model.Acronym{{Term: "SMF",
		Expansion: "Session Management Function", FirstRelease: "20.2.0",
		LastRelease: "20.2.0", SourceSeries: "23.501"}}); err != nil {
		t.Fatalf("new row: %v", err)
	} else if !changed {
		t.Error("a new term was not reported as a write")
	}
	if n := rowCount(t, s, "acronyms"); n != countBefore+1 {
		t.Errorf("a new term was not written: %d rows, want %d", n, countBefore+1)
	}
}

func rowCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
