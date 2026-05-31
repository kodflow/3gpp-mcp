package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

func buildShard(t *testing.T, path string, fn func(*store.Store)) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	fn(st)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

func cl(id uint64, spec, rel, ver, path string) model.Clause {
	return model.Clause{ChunkID: id, SpecID: spec, Release: rel, Version: ver, ClausePath: path, Heading: "h", Text: "t"}
}

// TestMergeSeriesReleaseScope is the non-overwrite guard for sub-sharding: two
// sub-shards of the SAME series but different releases must both survive an
// incremental merge, with no bucket clobbering another, no clause/CR duplication,
// and neighbouring series untouched.
func TestMergeSeriesReleaseScope(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := filepath.Join(dir, "base.duckdb")
	shardA := filepath.Join(dir, "shardA.duckdb") // 32 | Rel-17,18
	shardB := filepath.Join(dir, "shardB.duckdb") // 32 | Rel-19,20
	out := filepath.Join(dir, "out.duckdb")

	// All fixtures share the lexical pipeline_version, like real ingest output —
	// the incremental gate now requires base and output pipelines to match.
	lexPV := model.PipelineVersion("")

	// base: series 32 (Rel-17, Rel-18) + a series-29 Rel-19 clause (isolation).
	buildShard(t, base, func(st *store.Store) {
		_ = st.SetMeta("pipeline_version", lexPV)
		_ = st.UpsertSpec(model.Spec{SpecID: "32.298", Series: "32", DocType: "TS"})
		_ = st.UpsertSpec(model.Spec{SpecID: "29.518", Series: "29", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "32.298", Release: "Rel-17", Version: "17.4.0"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "32.298", Release: "Rel-18", Version: "18.3.0"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "29.518", Release: "Rel-19", Version: "19.2.0"})
		_ = st.InsertClauses([]model.Clause{
			cl(1, "32.298", "Rel-17", "17.4.0", "1"),
			cl(2, "32.298", "Rel-18", "18.3.0", "2"),
			cl(3, "29.518", "Rel-19", "19.2.0", "1"),
		})
		_ = st.InsertChanges([]model.Change{
			{CRNumber: "C17", SpecID: "32.298", ToVersion: "17.4.0"},
			{CRNumber: "C18", SpecID: "32.298", ToVersion: "18.3.0"},
			{CRNumber: "C29", SpecID: "29.518", ToVersion: "19.2.0"},
		})
	})
	buildShard(t, shardA, func(st *store.Store) {
		_ = st.SetMeta("pipeline_version", lexPV)
		_ = st.UpsertSpec(model.Spec{SpecID: "32.298", Series: "32", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "32.298", Release: "Rel-17", Version: "17.4.0"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "32.298", Release: "Rel-18", Version: "18.3.0"})
		_ = st.InsertClauses([]model.Clause{
			cl(1, "32.298", "Rel-17", "17.4.0", "1"),
			cl(2, "32.298", "Rel-18", "18.3.0", "2"),
		})
		_ = st.InsertChanges([]model.Change{
			{CRNumber: "C17", SpecID: "32.298", ToVersion: "17.4.0"},
			{CRNumber: "C18", SpecID: "32.298", ToVersion: "18.3.0"},
		})
	})
	buildShard(t, shardB, func(st *store.Store) {
		_ = st.SetMeta("pipeline_version", lexPV)
		_ = st.UpsertSpec(model.Spec{SpecID: "32.298", Series: "32", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "32.298", Release: "Rel-19", Version: "19.2.0"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "32.298", Release: "Rel-20", Version: "20.0.0"})
		_ = st.InsertClauses([]model.Clause{
			cl(4, "32.298", "Rel-19", "19.2.0", "1"),
			cl(5, "32.298", "Rel-20", "20.0.0", "2"),
		})
		_ = st.InsertChanges([]model.Change{
			{CRNumber: "C19", SpecID: "32.298", ToVersion: "19.2.0"},
			{CRNumber: "C20", SpecID: "32.298", ToVersion: "20.0.0"},
		})
	})

	// Incremental merge: base + shardA + shardB. FTS off (no extension under test).
	if err := run(ctx, out, []string{shardA, shardB}, false, "", base, false, ""); err != nil {
		t.Fatal(err)
	}

	st, err := store.OpenReadOnly(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	db := st.DB()

	// 1. All four release buckets of 32.298 survive (no bucket overwrote another).
	got := map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT release FROM clauses WHERE spec_id='32.298'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var r string
		_ = rows.Scan(&r)
		got[r] = true
	}
	_ = rows.Close()
	for _, r := range []string{"Rel-17", "Rel-18", "Rel-19", "Rel-20"} {
		if !got[r] {
			t.Errorf("missing release bucket %s (got %v)", r, got)
		}
	}
	if len(got) != 4 {
		t.Errorf("want 4 release buckets for 32.298, got %v", got)
	}

	// 2. No clause duplication or loss (one clause per release here → 4 total).
	var n int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE spec_id='32.298'`).Scan(&n)
	if n != 4 {
		t.Errorf("want 4 clauses for 32.298, got %d (dup or loss)", n)
	}

	// 3. Neighbouring series 29 untouched.
	var n29 int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE spec_id LIKE '29.%'`).Scan(&n29)
	if n29 != 1 {
		t.Errorf("series 29 should stay intact (1 clause), got %d", n29)
	}

	// 4. changes: no duplicate CR rows; all 4 to_version majors present for 32.298.
	var crDup int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT cr_number FROM changes GROUP BY cr_number HAVING count(*) > 1)`).Scan(&crDup)
	if crDup != 0 {
		t.Errorf("duplicate CR rows after merge: %d", crDup)
	}
	var crMajors int
	_ = db.QueryRowContext(ctx, `SELECT count(DISTINCT split_part(to_version,'.',1)) FROM changes WHERE spec_id='32.298'`).Scan(&crMajors)
	if crMajors != 4 {
		t.Errorf("want 4 distinct to_version majors for 32.298, got %d", crMajors)
	}
}

// TestStripEmbeddings verifies that the --strip-embeddings flag DROPs the
// embedding column from the merged DB (so the lexical 'latest' release asset
// stays under the 2 GB GitHub cap when embed=true is in flight and shards
// carry FLOAT[1024] vectors). The vectorised channel is publish-vec on GHCR.
func TestStripEmbeddings(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	shard := filepath.Join(dir, "shard.duckdb")
	out := filepath.Join(dir, "merged.duckdb")

	// Build a shard carrying a populated embedding (the realistic case).
	buildShard(t, shard, func(st *store.Store) {
		_ = st.UpsertSpec(model.Spec{SpecID: "24.501", Series: "24", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-18", Version: "18.0.0"})
		_ = st.InsertClauses([]model.Clause{cl(1, "24.501", "Rel-18", "18.0.0", "5.1.1")})
		// SetEmbedding writes a non-NULL vector — strip must then DROP the whole column.
		v := make([]float32, 1024)
		v[0] = 0.42
		_ = st.SetEmbedding(ctx, 1, v)
		_ = st.SetMeta("embedding_model", "bge-m3")
		_ = st.SetMeta("embedding_dim", "1024")
		_ = st.SetMeta("embedding_count", "1")
		// Seed hnsw_state so the cleanup assertion is non-trivial: without this
		// the DELETE would be a no-op and the test would pass even if the strip
		// path forgot the key.
		_ = st.SetMeta("hnsw_state", "frozen")
	})

	if err := run(ctx, out, []string{shard}, false, "", "", true /* stripEmbeddings */, ""); err != nil {
		t.Fatal(err)
	}

	st, err := store.OpenReadOnly(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	db := st.DB()

	// Embedding column gone — query must fail.
	if _, err := db.QueryContext(ctx, `SELECT embedding FROM clauses LIMIT 1`); err == nil {
		t.Fatal("embedding column survived --strip-embeddings (lexical artifact would carry vectors)")
	}
	// Lexical surface intact: clauses still selectable; spec still present.
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM clauses`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("clauses lost after strip: n=%d err=%v", n, err)
	}
	// Vector-specific meta keys gone (the slim DB must not claim semantic capability).
	for _, k := range []string{"embedding_model", "embedding_dim", "embedding_count", "hnsw_state"} {
		var v string
		_ = db.QueryRowContext(ctx, `SELECT value FROM schema_meta WHERE key = ?`, k).Scan(&v)
		if v != "" {
			t.Errorf("meta %q = %q survived --strip-embeddings", k, v)
		}
	}
}
