package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// TestMergePipelineVersionGate: an incremental merge whose --base carries a
// different pipeline_version than the incoming shard must DROP the base and
// rebuild from the shard only (you can't mix data from different indexing
// mechanics — plan §15 invariant #2).
func TestMergePipelineVersionGate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := filepath.Join(dir, "base.duckdb")
	shard := filepath.Join(dir, "shard.duckdb")
	out := filepath.Join(dir, "out.duckdb")

	buildShard(t, base, func(st *store.Store) {
		_ = st.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "23.501", Release: "Rel-18", Version: "18.0.0"})
		_ = st.InsertClauses([]model.Clause{cl(1, "23.501", "Rel-18", "18.0.0", "1")})
		_ = st.SetMeta("pipeline_version", "OLD-PIPELINE")
	})
	buildShard(t, shard, func(st *store.Store) {
		_ = st.UpsertSpec(model.Spec{SpecID: "24.501", Series: "24", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-18", Version: "18.0.0"})
		_ = st.InsertClauses([]model.Clause{cl(1, "24.501", "Rel-18", "18.0.0", "1")})
		_ = st.SetMeta("pipeline_version", "NEW-PIPELINE")
	})

	if err := run(ctx, out, []string{shard}, false, "", base, false, "", "", false); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenReadOnly(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	var nBase, nShard int
	_ = st.DB().QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE spec_id='23.501'`).Scan(&nBase)
	_ = st.DB().QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE spec_id='24.501'`).Scan(&nShard)
	if nBase != 0 {
		t.Errorf("base data must be dropped on pipeline_version mismatch, got %d clauses", nBase)
	}
	if nShard != 1 {
		t.Errorf("shard data must be present, got %d clauses", nShard)
	}
}

// TestMergePipelineVersionGateEmptyBase: a base that predates pipeline_version
// (no meta) against a versioned shard is treated as incompatible (rebuild).
func TestMergePipelineVersionGateEmptyBase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := filepath.Join(dir, "base.duckdb")
	shard := filepath.Join(dir, "shard.duckdb")
	out := filepath.Join(dir, "out.duckdb")

	buildShard(t, base, func(st *store.Store) { // NO pipeline_version (legacy base)
		_ = st.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "23.501", Release: "Rel-18", Version: "18.0.0"})
		_ = st.InsertClauses([]model.Clause{cl(1, "23.501", "Rel-18", "18.0.0", "1")})
	})
	buildShard(t, shard, func(st *store.Store) {
		_ = st.UpsertSpec(model.Spec{SpecID: "24.501", Series: "24", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-18", Version: "18.0.0"})
		_ = st.InsertClauses([]model.Clause{cl(1, "24.501", "Rel-18", "18.0.0", "1")})
		_ = st.SetMeta("pipeline_version", "NEW-PIPELINE")
	})
	if err := run(ctx, out, []string{shard}, false, "", base, false, "", "", false); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenReadOnly(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var nBase int
	_ = st.DB().QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE spec_id='23.501'`).Scan(&nBase)
	if nBase != 0 {
		t.Errorf("legacy base (no pipeline_version) must be dropped, got %d clauses", nBase)
	}
}
