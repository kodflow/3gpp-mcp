package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
	"github.com/kodflow/3gpp-mcp/internal/subjectmeta"
)

// TestIncrementalAppendKeepsUntouchedSeries is the cross-run append regression
// guard. It reproduces the production delta flow:
//
//	run 1 (full)  → base = whole corpus, published to `latest`.
//	run 2 (delta) → discover emits only the changed series; build-shard rebuilds
//	                JUST that series; merge --base folds the published base and
//	                replaces only the changed series' buckets.
//
// The landmine: if the merged output does NOT carry pipeline_version, then on
// run 2 the base's pipeline_version reads back as "" while the shard carries a
// real one → the gate declares a mismatch → incremental is abandoned → merge
// folds ONLY the delta shard → `latest` is clobbered with just the changed
// series and the rest of the corpus is destroyed. This test fails (series 23
// vanishes) until merge propagates pipeline_version to its output.
func TestIncrementalAppendKeepsUntouchedSeries(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := filepath.Join(dir, "base.duckdb")
	delta := filepath.Join(dir, "delta-24.duckdb")
	out := filepath.Join(dir, "out.duckdb")

	// A real lexical pipeline stamps PipelineVersion("") on both base and shards.
	lexPV := model.PipelineVersion("")

	// run 1 output (acts as the published `latest` base): two series, 23 + 24,
	// stamped with the lexical pipeline_version exactly like a real merge would.
	buildShard(t, base, func(st *store.Store) {
		_ = st.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS"})
		_ = st.UpsertSpec(model.Spec{SpecID: "24.501", Series: "24", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "23.501", Release: "Rel-18", Version: "18.0.0"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-18", Version: "18.0.0"})
		_ = st.InsertClauses([]model.Clause{
			cl(1, "23.501", "Rel-18", "18.0.0", "1"),
			cl(2, "24.501", "Rel-18", "18.0.0", "1"),
		})
		_ = st.SetMeta("pipeline_version", lexPV)
	})

	// run 2 delta shard: series 24 only, bumped to a newer version, same pipeline.
	buildShard(t, delta, func(st *store.Store) {
		_ = st.UpsertSpec(model.Spec{SpecID: "24.501", Series: "24", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-18", Version: "18.2.0"})
		_ = st.InsertClauses([]model.Clause{
			cl(1, "24.501", "Rel-18", "18.2.0", "1"),
			cl(2, "24.501", "Rel-18", "18.2.0", "2"),
		})
		_ = st.SetMeta("pipeline_version", lexPV)
	})

	if err := run(ctx, out, []string{delta}, false, "", base, false, "", ""); err != nil {
		t.Fatal(err)
	}

	st, err := store.OpenReadOnly(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	db := st.DB()

	// 1. Untouched series 23 MUST survive the delta merge (the whole point of append).
	var n23 int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE spec_id LIKE '23.%'`).Scan(&n23)
	if n23 != 1 {
		t.Fatalf("untouched series 23 lost in incremental merge: got %d clauses, want 1 "+
			"(pipeline_version not propagated → base dropped → corpus data loss)", n23)
	}

	// 2. Changed series 24 replaced by the delta (new version, 2 clauses, old gone).
	var n24, nOld int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE spec_id='24.501'`).Scan(&n24)
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE spec_id='24.501' AND version='18.0.0'`).Scan(&nOld)
	if n24 != 2 {
		t.Errorf("series 24 should hold the delta's 2 clauses, got %d", n24)
	}
	if nOld != 0 {
		t.Errorf("old 24.501 version should be replaced, found %d stale clauses", nOld)
	}

	// 3. The merged output MUST itself carry the pipeline_version, so the NEXT
	//    delta run reuses it as a base instead of full-rebuilding (and clobbering).
	if got := st.GetMeta(ctx, "pipeline_version"); got != lexPV {
		t.Errorf("merged output pipeline_version = %q, want %q (next run's --base gate needs it)", got, lexPV)
	}
}

// TestStripEmbeddingsStampsLexicalPipeline verifies that an embed-run merge,
// which strips vectors to keep the lexical `latest` asset slim, stamps the
// LEXICAL pipeline_version on the output (not the shard's bge-m3 one). Without
// this, the next lexical delta would see a pipeline mismatch against the
// published base and full-rebuild (clobbering the corpus).
func TestStripEmbeddingsStampsLexicalPipeline(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	shard := filepath.Join(dir, "shard.duckdb")
	out := filepath.Join(dir, "out.duckdb")

	embedPV := model.PipelineVersion("bge-m3")
	buildShard(t, shard, func(st *store.Store) {
		_ = st.UpsertSpec(model.Spec{SpecID: "24.501", Series: "24", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-18", Version: "18.0.0"})
		_ = st.InsertClauses([]model.Clause{cl(1, "24.501", "Rel-18", "18.0.0", "1")})
		v := make([]float32, 1024)
		v[0] = 0.42
		_ = st.SetEmbedding(ctx, 1, v)
		_ = st.SetMeta("pipeline_version", embedPV)
		_ = st.SetMeta("embedding_model", "bge-m3")
	})

	if err := run(ctx, out, []string{shard}, false, "", "", true /* stripEmbeddings */, "", ""); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenReadOnly(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	want := model.PipelineVersion("") // lexical, because vectors were stripped
	if got := st.GetMeta(ctx, "pipeline_version"); got != want {
		t.Errorf("stripped output pipeline_version = %q, want lexical %q", got, want)
	}
}

// TestMergeEmitsSubjectIndex verifies merge stamps each subject's footprint into
// the output meta AND writes subject-index.json matching the current code — the
// artifact discover diffs to detect a changed subject (plan TROU #1).
func TestMergeEmitsSubjectIndex(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	shard := filepath.Join(dir, "shard.duckdb")
	out := filepath.Join(dir, "out.duckdb")
	subjIdx := filepath.Join(dir, "subject-index.json")

	// Cover BOTH subject series (33 = li, 21 = glossary) so a full build advances
	// every subject's footprint to the current code (a real --all includes all
	// series; a narrow build would only advance the built subjects — see the
	// not-rebuilt test).
	buildShard(t, shard, func(st *store.Store) {
		_ = st.SetMeta("pipeline_version", model.PipelineVersion(""))
		_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS"})
		_ = st.UpsertSpec(model.Spec{SpecID: "21.905", Series: "21", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-18", Version: "18.0.0"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "21.905", Release: "Rel-18", Version: "18.0.0"})
		_ = st.InsertClauses([]model.Clause{
			cl(1, "33.128", "Rel-18", "18.0.0", "6"),
			cl(2, "21.905", "Rel-18", "18.0.0", "3"),
		})
	})

	if err := run(ctx, out, []string{shard}, false, "", "", false, subjIdx, ""); err != nil {
		t.Fatal(err)
	}

	// subject-index.json must equal the current code's footprints.
	b, err := os.ReadFile(subjIdx)
	if err != nil {
		t.Fatalf("subject-index not written: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	want := subjectmeta.Index()
	if len(got) != len(want) {
		t.Fatalf("subject-index has %d entries, want %d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("subject-index[%q] = %q, want %q", k, got[k], v)
		}
	}

	// Output meta self-describes the footprints too.
	st, err := store.OpenReadOnly(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	for _, m := range subjectmeta.All {
		if got := st.GetMeta(ctx, "subject_fp_"+m.Name); got != subjectmeta.Footprint(m) {
			t.Errorf("meta subject_fp_%s = %q, want %q", m.Name, got, subjectmeta.Footprint(m))
		}
	}
}

// TestSubjectFootprintNotAdvancedWhenSeriesNotRebuilt is the "footprint must not
// lie" guard. An incremental merge whose shards DON'T include a subject's series
// must NOT advance that subject's published footprint to the current code — it
// must carry the base's value forward, so the next discover still rebuilds the
// not-yet-updated subject. (Reproduces the explicit-series_scope hazard.)
func TestSubjectFootprintNotAdvancedWhenSeriesNotRebuilt(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := filepath.Join(dir, "base.duckdb")
	shard := filepath.Join(dir, "shard-24.duckdb")
	out := filepath.Join(dir, "out.duckdb")
	subjIdx := filepath.Join(dir, "subject-index.json")
	lexPV := model.PipelineVersion("")

	// Base carries a STALE li footprint (pretend li was last built at an old
	// version) plus series 33 (li's series) and series 24.
	const staleLIFP = "oldli0000000"
	buildShard(t, base, func(st *store.Store) {
		_ = st.SetMeta("pipeline_version", lexPV)
		_ = st.SetMeta("subject_fp_li", staleLIFP)
		_ = st.SetMeta("subject_fp_glossary", "oldgloss0000")
		_ = st.UpsertSpec(model.Spec{SpecID: "33.128", Series: "33", DocType: "TS"})
		_ = st.UpsertSpec(model.Spec{SpecID: "24.501", Series: "24", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "33.128", Release: "Rel-18", Version: "18.0.0"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-18", Version: "18.0.0"})
		_ = st.InsertClauses([]model.Clause{
			cl(1, "33.128", "Rel-18", "18.0.0", "6"),
			cl(2, "24.501", "Rel-18", "18.0.0", "5"),
		})
	})
	// Delta rebuilds ONLY series 24 — li's series (33) is untouched this run.
	buildShard(t, shard, func(st *store.Store) {
		_ = st.SetMeta("pipeline_version", lexPV)
		_ = st.UpsertSpec(model.Spec{SpecID: "24.501", Series: "24", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-18", Version: "18.1.0"})
		_ = st.InsertClauses([]model.Clause{cl(1, "24.501", "Rel-18", "18.1.0", "5")})
	})

	if err := run(ctx, out, []string{shard}, false, "", base, false, subjIdx, ""); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(subjIdx)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	// li's series (33) was NOT rebuilt → its footprint must stay the base's stale
	// value, NOT advance to current code. Otherwise the index would claim li is
	// up to date while series 33 still holds the old extraction.
	if got["li"] != staleLIFP {
		t.Errorf("li footprint = %q, want carried-forward base %q (33 not rebuilt → must not advance)", got["li"], staleLIFP)
	}
}
