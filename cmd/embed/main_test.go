package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/embed"
	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// localEmbedder forces EMBEDDER=local behaviour without env: deterministic
// hash-based vectors, no model download, Enabled()==true.
func localEmbedder() embed.Embedder { return embed.Local{} }

func seedClause(t *testing.T, st *store.Store, id uint64, spec, rel, ver, heading, text string) {
	t.Helper()
	_ = st.UpsertSpec(model.Spec{SpecID: spec, Series: spec[:2], DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: spec, Release: rel, Version: ver})
	if err := st.InsertClauses([]model.Clause{
		{ChunkID: id, SpecID: spec, Release: rel, Version: ver, ClausePath: "1", Heading: heading, Text: text},
	}); err != nil {
		t.Fatal(err)
	}
}

func nullEmb(t *testing.T, dbPath string) int {
	t.Helper()
	st, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	n, err := st.CountNullEmbeddings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// TestEmbedIsMicroGranular is the core proof: the decoupled embed step embeds
// only what changed.
//
//  1. fresh lexical DB (3 clauses, no vectors) → embed → 3 embedded, 0 null.
//  2. re-run embed on the unchanged DB → 0 embedded (all hashes current).
//  3. mutate ONE clause's text → embed → exactly 1 re-embedded, the other 2 skipped.
func TestEmbedIsMicroGranular(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "x.duckdb")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	seedClause(t, st, 1, "23.501", "Rel-19", "19.0.0", "5.1", "system architecture")
	seedClause(t, st, 2, "24.501", "Rel-19", "19.0.0", "4.2", "NAS protocol")
	seedClause(t, st, 3, "29.502", "Rel-19", "19.0.0", "6.1", "SMF services")
	_ = st.Close()

	e := localEmbedder()

	// 1. First embed: all 3 are candidates, all 3 embedded.
	rep, err := run(ctx, dbPath, e, "", true)
	if err != nil {
		t.Fatalf("run1: %v", err)
	}
	if rep.Candidates != 3 || rep.Embedded != 3 {
		t.Fatalf("run1: candidates=%d embedded=%d, want 3/3", rep.Candidates, rep.Embedded)
	}
	if n := nullEmb(t, dbPath); n != 0 {
		t.Fatalf("run1: %d null embeddings after embedding all, want 0", n)
	}

	// 2. Re-run, nothing changed: 0 embedded (every hash already current).
	rep, err = run(ctx, dbPath, e, "", true)
	if err != nil {
		t.Fatalf("run2: %v", err)
	}
	if rep.Embedded != 0 {
		t.Errorf("run2 (no change): embedded=%d, want 0 (micro-granular skip failed)", rep.Embedded)
	}
	if rep.Skipped != 3 {
		t.Errorf("run2: skipped=%d, want 3", rep.Skipped)
	}

	// 3. Mutate exactly one clause's text → only that clause re-embeds. The DB now
	// carries a frozen HNSW (run1/run2), so we must drop it before UPDATE-ing the
	// table — exactly what cmd/embed itself does via PrepareForEmbedUpdate.
	st, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PrepareForEmbedUpdate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE clauses SET text = 'system architecture REVISED' WHERE chunk_id = 1`); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	rep, err = run(ctx, dbPath, e, "", true)
	if err != nil {
		t.Fatalf("run3: %v", err)
	}
	if rep.Embedded != 1 {
		t.Errorf("run3 (1 clause changed): embedded=%d, want exactly 1", rep.Embedded)
	}
	if rep.Skipped != 2 {
		t.Errorf("run3: skipped=%d, want 2 (unchanged clauses reused)", rep.Skipped)
	}
}

// TestEmbedFloorSkipsOldReleases verifies --embed-floor leaves below-floor clauses
// lexical-only (no vector) while embedding at/above the floor.
func TestEmbedFloorSkipsOldReleases(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "x.duckdb")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	seedClause(t, st, 1, "23.501", "Rel-17", "17.0.0", "5.1", "old release clause")
	seedClause(t, st, 2, "23.501", "Rel-19", "19.0.0", "5.2", "new release clause")
	_ = st.Close()

	rep, err := run(ctx, dbPath, localEmbedder(), "Rel-19", true)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.Candidates != 1 || rep.Embedded != 1 {
		t.Fatalf("floor: candidates=%d embedded=%d, want 1/1 (only Rel-19)", rep.Candidates, rep.Embedded)
	}
	// One clause (Rel-17) stays without a vector.
	if n := nullEmb(t, dbPath); n != 1 {
		t.Errorf("floor: %d null embeddings, want 1 (the below-floor clause)", n)
	}
	// The global null count is 1 (the below-floor clause), but the CI completeness
	// gate keys on NullAtFloor, which MUST be 0 — every at/above-floor clause is
	// vectorised. This is the exact regression that failed the embed run: the gate
	// must NOT count intentionally-skipped below-floor clauses as a failure.
	if rep.NullAfter != 1 {
		t.Errorf("floor: NullAfter=%d, want 1 (global, incl. below-floor)", rep.NullAfter)
	}
	if rep.NullAtFloor != 0 {
		t.Errorf("floor: NullAtFloor=%d, want 0 (no at/above-floor clause left unembedded)", rep.NullAtFloor)
	}
}
