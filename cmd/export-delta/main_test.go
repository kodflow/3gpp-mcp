package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// seedDB builds a tiny source DB: 4 clauses across 2 series, 2 of which already
// carry a vector (so the delta must contain exactly the 2 NULL-embedding ones).
func seedDB(t *testing.T, path string) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS"})
	_ = st.UpsertSpec(model.Spec{SpecID: "33.501", Series: "33", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "23.501", Release: "Rel-18", Version: "18.3.0"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.501", Release: "Rel-18", Version: "18.2.0"})
	if err := st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-18", Version: "18.3.0", ClausePath: "5.1", Heading: "a", Text: "alpha"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-18", Version: "18.3.0", ClausePath: "5.2", Heading: "b", Text: "beta"},
		{ChunkID: 3, SpecID: "33.501", Release: "Rel-18", Version: "18.2.0", ClausePath: "6.1", Heading: "c", Text: "gamma"},
		{ChunkID: 4, SpecID: "33.501", Release: "Rel-18", Version: "18.2.0", ClausePath: "6.2", Heading: "d", Text: "delta"},
	}); err != nil {
		t.Fatal(err)
	}
	// Vectorise chunk 1 and 3 → they must NOT appear in the delta.
	vec := make([]float32, 1024)
	vec[0] = 0.5
	if err := st.SetEmbeddingWithHash(context.Background(), 1, vec, "h1"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetEmbeddingWithHash(context.Background(), 3, vec, "h3"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestExportDeltaSelectsNullEmbeddings is the core contract: the delta carries
// exactly the clauses WHERE embedding IS NULL — with their chunk_id and text
// intact (the join key + the payload the GPU run needs) — and the catalogue.
func TestExportDeltaSelectsNullEmbeddings(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "full.duckdb")
	out := filepath.Join(dir, "delta.duckdb")
	seedDB(t, src)

	if err := run(ctx, src, out, "", false); err != nil {
		t.Fatalf("run: %v", err)
	}

	db, err := store.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var n int
	if err := db.DB().QueryRowContext(ctx, `SELECT count(*) FROM clauses`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("delta clause count = %d, want 2 (the NULL-embedding rows)", n)
	}
	// The chunk_ids must be exactly {2,4} and every delta clause must lack a vector.
	rows, err := db.DB().QueryContext(ctx, `SELECT chunk_id FROM clauses WHERE embedding IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		t.Fatal("delta contains a clause that already has an embedding — must be NULL-only")
	}
	var got2, got4 bool
	r2, err := db.DB().QueryContext(ctx, `SELECT chunk_id, text FROM clauses ORDER BY chunk_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r2.Close() }()
	for r2.Next() {
		var id uint64
		var text string
		if err := r2.Scan(&id, &text); err != nil {
			t.Fatal(err)
		}
		switch id {
		case 2:
			got2 = text == "beta"
		case 4:
			got4 = text == "delta"
		default:
			t.Fatalf("unexpected chunk_id %d in delta", id)
		}
	}
	if !got2 || !got4 {
		t.Fatalf("delta must carry chunk_ids {2,4} with their text; got2=%v got4=%v", got2, got4)
	}
	// Catalogue context copied whole.
	var specs int
	if err := db.DB().QueryRowContext(ctx, `SELECT count(*) FROM specs`).Scan(&specs); err != nil {
		t.Fatal(err)
	}
	if specs != 2 {
		t.Fatalf("specs copied = %d, want 2", specs)
	}
}

// TestExportDeltaSeriesFilter scopes the delta to one series.
func TestExportDeltaSeriesFilter(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "full.duckdb")
	out := filepath.Join(dir, "delta.duckdb")
	seedDB(t, src)

	if err := run(ctx, src, out, "33", false); err != nil {
		t.Fatalf("run: %v", err)
	}
	db, err := store.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var n int
	if err := db.DB().QueryRowContext(ctx, `SELECT count(*) FROM clauses`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	// Series 33 has clauses 3 (vectorised) and 4 (NULL) → only 4 in the delta.
	if n != 1 {
		t.Fatalf("series-33 delta clause count = %d, want 1 (chunk 4)", n)
	}
}
