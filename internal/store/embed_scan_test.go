package store

import (
	"context"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// drainScan reads (chunkID, release, storedHash) triples from a work-list query.
func drainScan(t *testing.T, s *Store, scan EmbedScan) (ids []uint64, rels []string) {
	t.Helper()
	rows, err := s.ClausesNeedingEmbedding(context.Background(), scan)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			id            uint64
			heading, text string
			release, hash string
		)
		if err := rows.Scan(&id, &heading, &text, &release, &hash); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		rels = append(rels, release)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids, rels
}

// TestClausesNeedingEmbeddingScan pins the SQL-pushdown work-list: recent-first
// ordering (the user's core ask), the release floor, series narrowing, limit, and
// resume-only filtering — all the levers a bounded Kaggle kernel relies on.
func TestClausesNeedingEmbeddingScan(t *testing.T) {
	ctx := context.Background()
	s := newMem(t)
	// chunk_id order deliberately ≠ release order, so ordering is the query's doing.
	if err := s.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-99", Version: "3.1.0", ClausePath: "1", Heading: "h", Text: "t"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-20", Version: "20.0.0", ClausePath: "2", Heading: "h", Text: "t"},
		{ChunkID: 3, SpecID: "33.128", Release: "Rel-18", Version: "18.5.0", ClausePath: "3", Heading: "h", Text: "t"},
		{ChunkID: 4, SpecID: "23.502", Release: "Rel-19", Version: "19.6.0", ClausePath: "4", Heading: "h", Text: "t"},
	}); err != nil {
		t.Fatal(err)
	}

	// Default: recent-release-first; Rel-99 must land LAST, not first.
	_, rels := drainScan(t, s, EmbedScan{})
	want := []string{"Rel-20", "Rel-19", "Rel-18", "Rel-99"}
	if len(rels) != len(want) {
		t.Fatalf("default scan len=%d want %d (%v)", len(rels), len(want), rels)
	}
	for i, w := range want {
		if rels[i] != w {
			t.Errorf("default order[%d]=%q want %q", i, rels[i], w)
		}
	}

	// OldestFirst → chunk_id ASC (resume-agnostic legacy order).
	ids, _ := drainScan(t, s, EmbedScan{OldestFirst: true})
	for i, w := range []uint64{1, 2, 3, 4} {
		if ids[i] != w {
			t.Errorf("oldest-first id[%d]=%d want %d", i, ids[i], w)
		}
	}

	// Floor Rel-19 (ordinal 19): only Rel-19 + Rel-20 survive.
	ord, _ := model.ReleaseOrdinal("Rel-19")
	_, rels = drainScan(t, s, EmbedScan{FloorOrd: ord})
	if len(rels) != 2 || rels[0] != "Rel-20" || rels[1] != "Rel-19" {
		t.Errorf("floor Rel-19 = %v, want [Rel-20 Rel-19]", rels)
	}

	// Series 23: only spec_ids starting "23" (chunks 1,2,4).
	ids, _ = drainScan(t, s, EmbedScan{SeriesPrefix: "23"})
	if len(ids) != 3 {
		t.Errorf("series 23 = %v, want 3 rows", ids)
	}

	// Limit 1 on recent-first → just the newest (Rel-20, chunk 2).
	ids, rels = drainScan(t, s, EmbedScan{Limit: 1})
	if len(ids) != 1 || ids[0] != 2 || rels[0] != "Rel-20" {
		t.Errorf("limit 1 = ids %v rels %v, want [2]/[Rel-20]", ids, rels)
	}

	// ResumeOnly: after embedding chunk 2, it drops out of the resume work-list.
	if err := s.SetEmbeddingWithHash(ctx, 2, make([]float32, 1024), "deadbeef"); err != nil {
		t.Fatal(err)
	}
	ids, _ = drainScan(t, s, EmbedScan{ResumeOnly: true})
	for _, id := range ids {
		if id == 2 {
			t.Errorf("ResumeOnly still returned embedded chunk 2: %v", ids)
		}
	}
	if len(ids) != 3 {
		t.Errorf("ResumeOnly = %v, want 3 still-NULL rows", ids)
	}
}

// TestResumeKeysOnVectorNotHash pins the disk-crash-recovery fix: resume keys on the
// embedding VECTOR, never on embedding_hash. A clause with a vector but an EMPTY hash
// (the exact state a partial DB surfaced after a Kaggle resume-dataset round-trip)
// must be treated as DONE and SKIPPED — a hash-keyed resume re-embedded everything.
func TestResumeKeysOnVectorNotHash(t *testing.T) {
	ctx := context.Background()
	s := newMem(t)
	if err := s.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-19", Version: "19.6.0", ClausePath: "1", Heading: "h", Text: "t"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-19", Version: "19.6.0", ClausePath: "2", Heading: "h", Text: "t"},
	}); err != nil {
		t.Fatal(err)
	}
	// chunk 1: vector present but hash EMPTY (the partial-DB / round-trip scenario).
	if err := s.SetEmbeddingsBatch(ctx, []uint64{1}, [][]float32{make([]float32, 1024)}, []string{""}); err != nil {
		t.Fatal(err)
	}
	// chunk 2: no vector → genuinely needs embedding.
	ids, _ := drainScan(t, s, EmbedScan{ResumeOnly: true})
	if len(ids) != 1 || ids[0] != 2 {
		t.Errorf("resume work-list = %v, want only [2] (chunk 1 has a vector → done, despite empty hash)", ids)
	}
}

// TestClausesNeedingEmbeddingReleaseSet pins the per-RELEASE-lot selector: an exact
// label set restricts the work-list to those releases only (the balanced-lot Kaggle
// shards rely on it), stays recent-first ordered, AND-combines with --series, and an
// empty set is a no-op (all releases). Non-contiguous sets — which a floor cannot
// express — are the whole point, so the assertions use one.
func TestClausesNeedingEmbeddingReleaseSet(t *testing.T) {
	s := newMem(t)
	if err := s.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-99", Version: "3.1.0", ClausePath: "1", Heading: "h", Text: "t"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-20", Version: "20.0.0", ClausePath: "2", Heading: "h", Text: "t"},
		{ChunkID: 3, SpecID: "33.128", Release: "Rel-18", Version: "18.5.0", ClausePath: "3", Heading: "h", Text: "t"},
		{ChunkID: 4, SpecID: "23.502", Release: "Rel-19", Version: "19.6.0", ClausePath: "4", Heading: "h", Text: "t"},
	}); err != nil {
		t.Fatal(err)
	}

	// Non-contiguous lot {Rel-20, Rel-18, Rel-99}: exactly those three, recent-first.
	_, rels := drainScan(t, s, EmbedScan{Releases: []string{"Rel-20", "Rel-18", "Rel-99"}})
	want := []string{"Rel-20", "Rel-18", "Rel-99"}
	if len(rels) != len(want) {
		t.Fatalf("release set = %v, want %v", rels, want)
	}
	for i, w := range want {
		if rels[i] != w {
			t.Errorf("release set order[%d]=%q want %q", i, rels[i], w)
		}
	}

	// AND-combined with series 23: Rel-18 lives only on 33.128, so it drops out.
	ids, _ := drainScan(t, s, EmbedScan{SeriesPrefix: "23", Releases: []string{"Rel-20", "Rel-18", "Rel-99"}})
	for _, id := range ids {
		if id == 3 {
			t.Errorf("series 23 ∩ release set wrongly kept 33.128 chunk 3: %v", ids)
		}
	}
	if len(ids) != 2 { // chunks 1 (Rel-99) + 2 (Rel-20), both on 23.501
		t.Errorf("series 23 ∩ {Rel-20,Rel-18,Rel-99} = %v, want 2 rows", ids)
	}

	// Empty set is a no-op: all four clauses surface.
	ids, _ = drainScan(t, s, EmbedScan{Releases: nil})
	if len(ids) != 4 {
		t.Errorf("empty release set = %v, want all 4 rows", ids)
	}
}

// TestEmptyTextClausesAreUnembeddable pins the convergence fix: heading-only / "void"
// clauses (empty text) can never carry a dense vector, so the embedder skips them.
// The work-list and every null-counter must agree — otherwise a per-series resume
// re-selects the same empty-text rows forever and complete=1 never fires (the bug
// that stalled the series-23 Kaggle campaign).
func TestEmptyTextClausesAreUnembeddable(t *testing.T) {
	ctx := context.Background()
	s := newMem(t)
	if err := s.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-19", Version: "19.6.0", ClausePath: "1", Heading: "Scope", Text: "real body"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-19", Version: "19.6.0", ClausePath: "2", Heading: "Void", Text: ""},
		{ChunkID: 3, SpecID: "23.501", Release: "Rel-19", Version: "19.6.0", ClausePath: "3", Heading: "Reserved", Text: "   "},
	}); err != nil {
		t.Fatal(err)
	}

	// Work-list (default AND resume) excludes the empty/whitespace-only clauses.
	for _, scan := range []EmbedScan{{}, {ResumeOnly: true}} {
		ids, _ := drainScan(t, s, scan)
		if len(ids) != 1 || ids[0] != 1 {
			t.Errorf("scan %+v work-list = %v, want only [1] (empty-text 2,3 excluded)", scan, ids)
		}
	}

	// Counters: only chunk 1 is a genuine missing vector; 2 and 3 are NULL by design.
	if n, err := s.CountNullEmbeddings(ctx); err != nil || n != 1 {
		t.Errorf("CountNullEmbeddings = (%d,%v), want (1,nil) — empty-text NULLs must not count", n, err)
	}
	if n, err := s.CountNullAtFloor(ctx, 0, ""); err != nil || n != 1 {
		t.Errorf("CountNullAtFloor = (%d,%v), want (1,nil)", n, err)
	}

	// After embedding the one real clause, the corpus is COMPLETE: every counter
	// reaches zero even though chunks 2 and 3 remain NULL.
	if err := s.SetEmbeddingWithHash(ctx, 1, make([]float32, 1024), "cafe"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountNullEmbeddings(ctx); n != 0 {
		t.Errorf("CountNullEmbeddings after embedding real clause = %d, want 0 (convergence)", n)
	}
	if n, _ := s.CountNullAtFloor(ctx, 0, ""); n != 0 {
		t.Errorf("CountNullAtFloor after embedding real clause = %d, want 0 (convergence)", n)
	}
	if ids, _ := drainScan(t, s, EmbedScan{ResumeOnly: true}); len(ids) != 0 {
		t.Errorf("resume work-list after embedding = %v, want [] (no more work)", ids)
	}
}
