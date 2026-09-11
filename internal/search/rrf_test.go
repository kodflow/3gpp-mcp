package search

import (
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestRRFChunkIDCollisionAcrossShards locks the fix for
// rrf-chunkid-collision-across-shards: chunk_id is a per-DB counter that each
// shard restarts at 1, so two DISTINCT clauses from two sub-bases share
// ChunkID=1. RRF must key on the logical (spec_id, release, version,
// clause_path) tuple — not chunk_id — so neither clause is dropped and rank
// mass is not misattributed across shards.
func TestRRFChunkIDCollisionAcrossShards(t *testing.T) {
	a := model.SearchHit{Clause: model.Clause{
		ChunkID: 1, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "5.2",
	}}
	b := model.SearchHit{Clause: model.Clause{
		ChunkID: 1, SpecID: "24.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "6.1",
	}}

	out := RRF(60, []model.SearchHit{a}, []model.SearchHit{b})

	if len(out) != 2 {
		t.Fatalf("both shard hits must survive RRF despite shared chunk_id=1, got %d result(s)", len(out))
	}
	seen := map[string]bool{}
	for _, h := range out {
		seen[h.Clause.SpecID] = true
	}
	if !seen["23.501"] || !seen["24.501"] {
		t.Fatalf("RRF dropped a colliding clause: want both 23.501 and 24.501, got %v", seen)
	}
}

// TestRRFSameClauseAcrossListsFuses verifies the SAME logical clause appearing
// in two lists (lexical + vector) is fused into one entry with summed score —
// distinct chunk_ids for the same logical clause must not split it, and the
// same logical key across lists must collapse to one row.
func TestRRFSameClauseAcrossListsFuses(t *testing.T) {
	// Same logical clause, but the two backends report different local chunk_ids.
	fromLexical := model.SearchHit{Clause: model.Clause{
		ChunkID: 7, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "5.2",
	}}
	fromVector := model.SearchHit{Clause: model.Clause{
		ChunkID: 42, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "5.2",
	}}

	out := RRF(60, []model.SearchHit{fromLexical}, []model.SearchHit{fromVector})

	if len(out) != 1 {
		t.Fatalf("same logical clause from two lists must fuse to one entry, got %d", len(out))
	}
	want := 2.0 * (1.0 / (60.0 + 1.0))
	if out[0].Score != want {
		t.Fatalf("fused score = %v, want %v (rank-1 in both lists)", out[0].Score, want)
	}
}

// TWO VERSIONS OF ONE CLAUSE WITH EQUAL FUSED SCORES COME OUT IN ONE ORDER. The
// tie-break used to stop at (spec_id, clause_path), below the fusion key, so the
// pair was ordered by map iteration and the served page changed between two
// identical calls. Every permutation of the input lists must give the same page.
func TestRRFOrdersEqualScoresTotally(t *testing.T) {
	v := func(ver string) model.SearchHit {
		return model.SearchHit{Clause: model.Clause{SpecID: "33.128", Release: "Rel-17", Version: ver, ClausePath: "6.2.3.2"}}
	}
	other := model.SearchHit{Clause: model.Clause{SpecID: "33.127", Release: "Rel-17", Version: "17.1.0", ClausePath: "6.2.3.3"}}
	// Rank 1 in one list each: equal fused scores for the two versions.
	a := []model.SearchHit{v("17.15.0"), other}
	b := []model.SearchHit{v("17.2.0"), other}
	want := RRF(60, a, b)
	if len(want) != 3 || want[0].Clause.SpecID != "33.127" {
		t.Fatalf("unexpected fusion %v", want)
	}
	for i := 0; i < 200; i++ { // map iteration order is randomised per range
		got := RRF(60, b, a)
		for j := range want {
			if rrfKey(got[j].Clause) != rrfKey(want[j].Clause) {
				t.Fatalf("run %d: position %d is %s, was %s — equal scores ordered by map iteration",
					i, j, rrfKey(got[j].Clause), rrfKey(want[j].Clause))
			}
		}
	}
	// The old order is kept where it decided: same score, different clause.
	x := model.SearchHit{Clause: model.Clause{SpecID: "33.128", Release: "Rel-18", Version: "18.0.0", ClausePath: "5.1"}}
	y := model.SearchHit{Clause: model.Clause{SpecID: "33.128", Release: "Rel-17", Version: "17.0.0", ClausePath: "6.1"}}
	if got := RRF(60, []model.SearchHit{y}, []model.SearchHit{x}); got[0].Clause.ClausePath != "5.1" {
		t.Errorf("equal scores in one spec are no longer ordered by clause_path first: %v", got)
	}
}
