package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/store"
)

// embeddedChunks returns the set of chunk_ids that currently carry a vector.
func embeddedChunks(t *testing.T, dbPath string) map[uint64]bool {
	t.Helper()
	st, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	rows, err := st.DB().QueryContext(context.Background(),
		`SELECT chunk_id FROM clauses WHERE embedding IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[uint64]bool{}
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out[id] = true
	}
	return out
}

// TestEmbedLimitRecentFirstResume proves the bounded-session levers a Kaggle shard
// relies on: --limit caps the work-list, recent-release-first means the cap lands
// on the NEWEST clauses, and --resume picks up exactly the remainder.
func TestEmbedLimitRecentFirstResume(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "x.duckdb")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	// chunk_id order (1,2,3) is the REVERSE of release recency, so a recent-first
	// limit must pick chunk 2 (Rel-19), not chunk 1.
	seedClause(t, st, 1, "23.501", "Rel-17", "17.0.0", "5.1", "old clause")
	seedClause(t, st, 2, "23.501", "Rel-19", "19.0.0", "5.2", "new clause")
	seedClause(t, st, 3, "24.501", "Rel-18", "18.0.0", "4.1", "mid clause")
	_ = st.Close()

	e := localEmbedder()

	// limit 1, recent-first, no HNSW (shard mode): embeds ONLY the newest (Rel-19).
	rep, err := run(ctx, dbPath, e, embedConfig{limit: 1})
	if err != nil {
		t.Fatalf("limited run: %v", err)
	}
	if rep.Candidates != 1 || rep.Embedded != 1 {
		t.Fatalf("limit 1: candidates=%d embedded=%d, want 1/1", rep.Candidates, rep.Embedded)
	}
	got := embeddedChunks(t, dbPath)
	if !got[2] || got[1] || got[3] {
		t.Errorf("limit 1 recent-first embedded %v, want only chunk 2 (Rel-19)", got)
	}

	// resume, no limit: embeds exactly the two remaining NULL clauses.
	rep, err = run(ctx, dbPath, e, embedConfig{resume: true})
	if err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if rep.Embedded != 2 {
		t.Errorf("resume: embedded=%d, want 2 (the remainder)", rep.Embedded)
	}
	if got := embeddedChunks(t, dbPath); !got[1] || !got[2] || !got[3] {
		t.Errorf("after resume, embedded=%v, want all three", got)
	}

	// resume again: nothing left.
	rep, err = run(ctx, dbPath, e, embedConfig{resume: true})
	if err != nil {
		t.Fatalf("resume2: %v", err)
	}
	if rep.Embedded != 0 {
		t.Errorf("resume2: embedded=%d, want 0 (idempotent)", rep.Embedded)
	}
}

// TestRunCountNull exercises the read-only --count-null-at-floor probe: it must
// not error on a valid DB and must signal failure on a missing one.
func TestRunCountNull(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "x.duckdb")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	seedClause(t, st, 1, "23.501", "Rel-19", "19.0.0", "5.1", "c")
	_ = st.Close()

	if rc := runCountNull(dbPath, "Rel-19", "23", "json"); rc != 0 {
		t.Errorf("runCountNull on valid DB = %d, want 0", rc)
	}
	if rc := runCountNull(filepath.Join(t.TempDir(), "nope.duckdb"), "", "", "text"); rc == 0 {
		t.Errorf("runCountNull on missing DB = 0, want non-zero")
	}
}

// TestEmbedOrderChunk pins --order chunk to the legacy chunk_id order.
func TestEmbedOrderChunk(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "x.duckdb")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	seedClause(t, st, 1, "23.501", "Rel-17", "17.0.0", "5.1", "old clause")
	seedClause(t, st, 2, "23.501", "Rel-19", "19.0.0", "5.2", "new clause")
	_ = st.Close()

	// order=chunk, limit 1 → chunk_id ASC picks chunk 1 (Rel-17), not the newest.
	if _, err := run(ctx, dbPath, localEmbedder(), embedConfig{limit: 1, order: "chunk"}); err != nil {
		t.Fatal(err)
	}
	if got := embeddedChunks(t, dbPath); !got[1] || got[2] {
		t.Errorf("order=chunk limit 1 embedded %v, want only chunk 1", got)
	}
}
