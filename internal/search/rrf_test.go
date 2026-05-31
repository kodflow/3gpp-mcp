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
