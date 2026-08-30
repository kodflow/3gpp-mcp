package store

import (
	"context"
	"path/filepath"
	"testing"
)

// indexOID identifies an index INSTANCE. DuckDB does not record the WITH clause an
// HNSW index was created with — duckdb_indexes().sql reads back as
// "CREATE INDEX clauses_hnsw ON clauses USING HNSW (embedding);", with the
// parameters dropped — so the parameters themselves cannot be asserted from the
// catalogue. What can be asserted is whether the index was REPLACED, and a rebuild
// is the only way new parameters can take effect.
func indexOID(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	var oid int64
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT index_oid FROM duckdb_indexes() WHERE index_name = ?`, name).Scan(&oid); err != nil {
		t.Fatalf("index_oid for %s: %v", name, err)
	}
	return oid
}

// TestHNSWRebuildsOnlyWhenParametersChange pins both halves of the bargain.
//
// `CREATE INDEX IF NOT EXISTS` succeeds by doing nothing when the index is already
// there, while the freeze markers are written either way — so a build asked for new
// parameters used to leave the corpus claiming an M/ef_construction/ef_search its
// graph was never built to. The step's fingerprint covers all three (so it re-runs),
// and it reported success: the fingerprint fix made the step run without making the
// run effective.
//
// The opposite mistake — dropping unconditionally — would pay a full rebuild on
// every run of a step whose whole point is to be skippable, so the no-change case is
// pinned too.
func TestHNSWRebuildsOnlyWhenParametersChange(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "params.duckdb")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	seedEmbedded(t, st)

	t.Setenv("HNSW_M", "16")
	t.Setenv("HNSW_EF_CONSTRUCTION", "64")
	t.Setenv("HNSW_EF_SEARCH", "64")
	if err := st.BuildAndFreezeHNSW(ctx, "bge-m3"); err != nil {
		t.Fatalf("first build: %v", err)
	}
	first := indexOID(t, st, "clauses_hnsw")
	if got := st.GetMeta(ctx, "hnsw_m"); got != "16" {
		t.Fatalf("hnsw_m after first build = %q, want 16", got)
	}

	// Same parameters: nothing may be rebuilt.
	if err := st.BuildAndFreezeHNSW(ctx, "bge-m3"); err != nil {
		t.Fatalf("idempotent build: %v", err)
	}
	if again := indexOID(t, st, "clauses_hnsw"); again != first {
		t.Errorf("index was rebuilt with unchanged parameters: oid %d -> %d", first, again)
	}

	// Changed parameters: the graph must actually be replaced, not just re-stamped.
	t.Setenv("HNSW_M", "32")
	t.Setenv("HNSW_EF_CONSTRUCTION", "128")
	t.Setenv("HNSW_EF_SEARCH", "128")
	if err := st.BuildAndFreezeHNSW(ctx, "bge-m3"); err != nil {
		t.Fatalf("rebuild after parameter change: %v", err)
	}
	rebuilt := indexOID(t, st, "clauses_hnsw")
	if rebuilt == first {
		t.Error("parameters changed but the index was not rebuilt — the corpus now claims parameters its graph was not built to")
	}
	for k, want := range map[string]string{
		"hnsw_m": "32", "hnsw_ef_construction": "128", "hnsw_ef_search": "128",
	} {
		if got := st.GetMeta(ctx, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if st.GetMeta(ctx, "hnsw_state") != "frozen" {
		t.Errorf("hnsw_state = %q, want frozen", st.GetMeta(ctx, "hnsw_state"))
	}
	// The rebuilt index must still answer: a dropped-and-recreated graph that does
	// not serve is worse than the stale one it replaced.
	hits, err := st.SearchVectors(ctx, oneHot(20), SpecFilter{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Clause.ClausePath != "6.2" {
		t.Errorf("self-match after rebuild = %+v, want clause 6.2", hits)
	}
}

// TestHNSWRebuildOnCorpusThatAlreadyCarriesAnIndex reproduces the etsi.duckdb
// failure of 2026-08-30.
//
// The build sequence used to write schema_meta and CHECKPOINT before loading VSS.
// A checkpoint has to bind every index it flushes, so on a corpus that already
// carries an HNSW index — exactly what a re-embed campaign re-indexes — DuckDB
// refused it outright:
//
//	Failed to create checkpoint: Missing Extension Error: Cannot bind index
//	'clauses', unknown index type 'HNSW'.
//
// The 3GPP corpus passed the same code minutes earlier only because it carried no
// index at that moment, so a single-corpus test would have proved nothing. What
// makes this reproduce is the REOPEN: `store.Open` does not load VSS, which is the
// state freeze-hnsw actually starts from.
func TestHNSWRebuildOnCorpusThatAlreadyCarriesAnIndex(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reindex.duckdb")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seedEmbedded(t, st)
	if err := st.BuildAndFreezeHNSW(ctx, "bge-m3"); err != nil {
		t.Fatalf("first build: %v", err)
	}
	if !st.indexExists(ctx, "clauses_hnsw") {
		t.Fatal("index missing after the first build")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen the way freeze-hnsw does: a fresh connection with VSS NOT loaded,
	// onto a file that already carries an HNSW index.
	re, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = re.Close() }()
	if err := re.BuildAndFreezeHNSW(ctx, "bge-m3"); err != nil {
		t.Fatalf("re-index of a corpus that already carries an index: %v", err)
	}
	if got := re.GetMeta(ctx, "hnsw_state"); got != "frozen" {
		t.Errorf("hnsw_state = %q, want frozen", got)
	}
	hits, err := re.SearchVectors(ctx, oneHot(20), SpecFilter{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Clause.ClausePath != "6.2" {
		t.Errorf("self-match after re-index = %+v, want clause 6.2", hits)
	}
}
