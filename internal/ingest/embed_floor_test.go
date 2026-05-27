package ingest

import (
	"context"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestEmbedFloorConditional checks that, with a vector floor, only clauses
// at/above the floor get embeddings (lexical rows are untouched), AND that the
// stored vector belongs to the right clause even when below/above-floor clauses
// are interleaved within one batch (the parallel-ids alignment guard).
func TestEmbedFloorConditional(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	// Interleave below/above-floor releases in one batch; distinct texts so the
	// deterministic local embedder yields distinguishable vectors.
	clauses := []model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-14", Version: "14.0.0", ClausePath: "1", Heading: "old", Text: "legacy epc bearer"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-15", Version: "15.0.0", ClausePath: "2", Heading: "floor", Text: "amf registration procedure five"},
		{ChunkID: 3, SpecID: "23.501", Release: "Rel-14", Version: "14.0.0", ClausePath: "3", Heading: "old2", Text: "gprs tunnelling protocol"},
		{ChunkID: 4, SpecID: "23.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "4", Heading: "new", Text: "network slicing admission control"},
	}
	if err := st.InsertClauses(clauses); err != nil {
		t.Fatal(err)
	}

	floor, _ := model.ReleaseOrdinal("Rel-15") // = 15
	stat := &Stats{}
	if err := embedClauses(ctx, st, embed.Local{}, clauses, floor, stat); err != nil {
		t.Fatal(err)
	}
	if !stat.Vectors {
		t.Fatal("stat.Vectors should be true (some clauses were embedded)")
	}

	// Exactly chunks 2 and 4 (>= Rel-15) carry embeddings; 1 and 3 stay NULL.
	got := map[uint64]bool{}
	rows, err := st.DB().QueryContext(ctx, `SELECT chunk_id FROM clauses WHERE embedding IS NOT NULL ORDER BY chunk_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got[id] = true
	}
	_ = rows.Close()
	if len(got) != 2 || !got[2] || !got[4] {
		t.Fatalf("embedded set = %v, want {2,4}", got)
	}

	// Alignment: clause 4's own vector must rank clause 4 first among {2,4}
	// (would fail if the skipped below-floor clauses misaligned ids↔vectors).
	v4, err := embed.Local{}.Embed(ctx, []string{clauses[3].Heading + "\n" + clauses[3].Text})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := st.SearchVectorsAmong(ctx, v4[0], []uint64{2, 4}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Clause.ChunkID != 4 {
		t.Fatalf("clause-4 vector should rank chunk 4 first, got %+v", hits)
	}
}
