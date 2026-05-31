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
		// The Change-History annex is CUMULATIVE per spec: each release sub-shard
		// ingests the WHOLE annex at its latest doc, not a release-split subset
		// (the prior fixture's per-release split was unrealistic and masked the
		// duplication bug — see finding changes-purge-major-vs-cumulative-annex-dup).
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
		// shardB is the LATEST shard for 32.298, so its cumulative annex carries
		// EVERY major (15..20). The spec-scoped purge clears 32.298 before this
		// fold, so the final DB holds exactly one copy of the full annex.
		_ = st.InsertChanges([]model.Change{
			{CRNumber: "C17", SpecID: "32.298", ToVersion: "17.4.0"},
			{CRNumber: "C18", SpecID: "32.298", ToVersion: "18.3.0"},
			{CRNumber: "C19", SpecID: "32.298", ToVersion: "19.2.0"},
			{CRNumber: "C20", SpecID: "32.298", ToVersion: "20.0.0"},
		})
	})

	// Incremental merge: base + shardA + shardB. FTS off (no extension under test).
	if err := run(ctx, out, []string{shardA, shardB}, false, "", base, false, "", ""); err != nil {
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

	if err := run(ctx, out, []string{shard}, false, "", "", true /* stripEmbeddings */, "", ""); err != nil {
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

// TestMergeChangesCumulativeAnnexIdempotent locks the PR-4 primary fix: a
// per-release sub-shard ingests the FULL cumulative Change-History annex (CRs
// spanning many majors), but the prior purge cleared only the shard's own
// release-major — so sub-floor CRs already in the base were re-inserted and
// duplicated on every delta. The fix purges `changes` by spec_id before the
// fold, so delete ⊇ insert. We assert no duplicate CRs AND that re-running the
// SAME delta twice keeps the changes-row count stable (true idempotency).
func TestMergeChangesCumulativeAnnexIdempotent(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := filepath.Join(dir, "base.duckdb")
	shard := filepath.Join(dir, "shard.duckdb")
	out1 := filepath.Join(dir, "out1.duckdb")
	out2 := filepath.Join(dir, "out2.duckdb")
	lexPV := model.PipelineVersion("")

	// Base: 24.501 already holds the cumulative annex majors 16,17,18,19 (from a
	// prior full build) plus its Rel-19 clause/version.
	annex := func() []model.Change {
		return []model.Change{
			{CRNumber: "C16", CRRevision: 1, SpecID: "24.501", ToVersion: "16.9.0"},
			{CRNumber: "C17", CRRevision: 1, SpecID: "24.501", ToVersion: "17.5.0"},
			{CRNumber: "C18", CRRevision: 1, SpecID: "24.501", ToVersion: "18.4.0"},
			{CRNumber: "C19", CRRevision: 1, SpecID: "24.501", ToVersion: "19.2.0"},
		}
	}
	buildShard(t, base, func(st *store.Store) {
		_ = st.SetMeta("pipeline_version", lexPV)
		_ = st.UpsertSpec(model.Spec{SpecID: "24.501", Series: "24", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-19", Version: "19.2.0"})
		_ = st.InsertClauses([]model.Clause{cl(1, "24.501", "Rel-19", "19.2.0", "5.1")})
		_ = st.InsertChanges(annex())
	})
	// Delta shard: ONLY Rel-19 in spec_versions, but the FULL cumulative annex in
	// changes (this is exactly what real ingest produces — the annex is per-spec
	// cumulative regardless of the release filter).
	buildShard(t, shard, func(st *store.Store) {
		_ = st.SetMeta("pipeline_version", lexPV)
		_ = st.UpsertSpec(model.Spec{SpecID: "24.501", Series: "24", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-19", Version: "19.3.0"})
		_ = st.InsertClauses([]model.Clause{cl(1, "24.501", "Rel-19", "19.3.0", "5.1")})
		_ = st.InsertChanges(annex())
	})

	if err := run(ctx, out1, []string{shard}, false, "", base, false, "", ""); err != nil {
		t.Fatal(err)
	}
	dupCount := func(path string) (dup, total int) {
		st, err := store.OpenReadOnly(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = st.Close() }()
		_ = st.DB().QueryRowContext(ctx,
			`SELECT count(*) FROM (SELECT spec_id, cr_number, cr_revision, to_version
			 FROM changes GROUP BY spec_id, cr_number, cr_revision, to_version HAVING count(*) > 1)`).Scan(&dup)
		_ = st.DB().QueryRowContext(ctx, `SELECT count(*) FROM changes WHERE spec_id='24.501'`).Scan(&total)
		return dup, total
	}
	dup1, total1 := dupCount(out1)
	if dup1 != 0 {
		t.Errorf("first delta merge produced %d duplicate CR groups (want 0)", dup1)
	}
	if total1 != 4 {
		t.Errorf("first delta merge: want 4 CR rows for 24.501, got %d", total1)
	}

	// Re-run the SAME delta against the freshly-published out1 as the new base.
	if err := run(ctx, out2, []string{shard}, false, "", out1, false, "", ""); err != nil {
		t.Fatal(err)
	}
	dup2, total2 := dupCount(out2)
	if dup2 != 0 {
		t.Errorf("second delta merge produced %d duplicate CR groups (want 0)", dup2)
	}
	if total2 != total1 {
		t.Errorf("changes row count grew across identical delta folds: %d -> %d (not idempotent)", total1, total2)
	}
}

// TestMergeSiblingSpecNotLost locks the PR-4 (spec_id, release) purge scope: a
// delta shard carrying a SUBSET of a (series, release) bucket must not wipe the
// sibling specs of that bucket from the base. The old (series, release) sweep
// deleted 24.008 Rel-19 when the shard rebuilt only 24.501 Rel-19.
func TestMergeSiblingSpecNotLost(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := filepath.Join(dir, "base.duckdb")
	shard := filepath.Join(dir, "shard.duckdb")
	out := filepath.Join(dir, "out.duckdb")
	lexPV := model.PipelineVersion("")

	// Base: two series-24 specs in the SAME (series, Rel-19) bucket.
	buildShard(t, base, func(st *store.Store) {
		_ = st.SetMeta("pipeline_version", lexPV)
		_ = st.UpsertSpec(model.Spec{SpecID: "24.501", Series: "24", DocType: "TS"})
		_ = st.UpsertSpec(model.Spec{SpecID: "24.008", Series: "24", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-19", Version: "19.2.0"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.008", Release: "Rel-19", Version: "19.1.0"})
		_ = st.InsertClauses([]model.Clause{
			cl(1, "24.501", "Rel-19", "19.2.0", "5.1"),
			cl(2, "24.008", "Rel-19", "19.1.0", "4.1"),
		})
		_ = st.InsertChanges([]model.Change{
			{CRNumber: "C501", SpecID: "24.501", ToVersion: "19.2.0"},
			{CRNumber: "C008", SpecID: "24.008", ToVersion: "19.1.0"},
		})
	})
	// Delta shard: ONLY 24.501 Rel-19 changed/rebuilt (subset of the bucket).
	buildShard(t, shard, func(st *store.Store) {
		_ = st.SetMeta("pipeline_version", lexPV)
		_ = st.UpsertSpec(model.Spec{SpecID: "24.501", Series: "24", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-19", Version: "19.3.0"})
		_ = st.InsertClauses([]model.Clause{cl(1, "24.501", "Rel-19", "19.3.0", "5.1")})
		_ = st.InsertChanges([]model.Change{{CRNumber: "C501", SpecID: "24.501", ToVersion: "19.3.0"}})
	})

	if err := run(ctx, out, []string{shard}, false, "", base, false, "", ""); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenReadOnly(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	db := st.DB()

	// Sibling 24.008 Rel-19 clause + CR survive (never purged, never re-inserted).
	var sibClauses, sibCRs int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE spec_id='24.008'`).Scan(&sibClauses)
	if sibClauses != 1 {
		t.Errorf("sibling 24.008 clauses lost: want 1, got %d", sibClauses)
	}
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM changes WHERE spec_id='24.008'`).Scan(&sibCRs)
	if sibCRs != 1 {
		t.Errorf("sibling 24.008 CRs lost: want 1, got %d", sibCRs)
	}
	// The rebuilt spec advanced to its new version (its old bucket was replaced).
	var ver string
	_ = db.QueryRowContext(ctx, `SELECT version FROM spec_versions WHERE spec_id='24.501'`).Scan(&ver)
	if ver != "19.3.0" {
		t.Errorf("rebuilt 24.501 version: want 19.3.0, got %q", ver)
	}
}

// TestMergeAcronymReplacedOnSubjectChange locks the PR-4 acronym scoped purge: a
// glossary subject change re-extracts series 21's acronyms; the stale base row
// (a corrected/removed term) must be purged by source_series before the fold,
// not survive forever behind ON CONFLICT DO NOTHING.
func TestMergeAcronymReplacedOnSubjectChange(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := filepath.Join(dir, "base.duckdb")
	shard := filepath.Join(dir, "shard.duckdb")
	out := filepath.Join(dir, "out.duckdb")
	lexPV := model.PipelineVersion("")

	// Base: series 21 with a WRONG/stale acronym expansion (the bug a glossary-v2
	// bump would fix), tagged with its owning series provenance.
	buildShard(t, base, func(st *store.Store) {
		_ = st.SetMeta("pipeline_version", lexPV)
		_ = st.UpsertSpec(model.Spec{SpecID: "21.905", Series: "21", DocType: "TR"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "21.905", Release: "Rel-18", Version: "18.0.0"})
		_ = st.InsertClauses([]model.Clause{cl(1, "21.905", "Rel-18", "18.0.0", "3.2")})
		_ = st.UpsertAcronym(model.Acronym{Term: "XYZ", Expansion: "Wrong Thing", SourceSeries: "21"})
	})
	// Delta shard: series 21 rebuilt; corrected acronym (different expansion → a
	// different PK, so without the purge BOTH rows would coexist).
	buildShard(t, shard, func(st *store.Store) {
		_ = st.SetMeta("pipeline_version", lexPV)
		_ = st.UpsertSpec(model.Spec{SpecID: "21.905", Series: "21", DocType: "TR"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "21.905", Release: "Rel-18", Version: "18.1.0"})
		_ = st.InsertClauses([]model.Clause{cl(1, "21.905", "Rel-18", "18.1.0", "3.2")})
		_ = st.UpsertAcronym(model.Acronym{Term: "XYZ", Expansion: "Correct Thing", SourceSeries: "21"})
	})

	if err := run(ctx, out, []string{shard}, false, "", base, false, "", ""); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenReadOnly(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	got, err := st.ResolveTerm(ctx, "XYZ")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 acronym row for XYZ after subject rebuild, got %d (%v)", len(got), got)
	}
	if got[0].Expansion != "Correct Thing" {
		t.Errorf("stale acronym survived merge: got %q, want %q", got[0].Expansion, "Correct Thing")
	}
}

// TestMergeDroppedBasePublishesEmptySubjectFootprint locks the PR-4
// subject-fp-honesty fix: when the pipeline_version gate drops the base
// (incremental=false over a discover-sized narrow matrix), an UNREBUILT
// subject's data is no longer in the merged DB, so its published footprint must
// be empty/absent — never the dropped base's value (which would lie and stop
// the next discover from healing it).
func TestMergeDroppedBasePublishesEmptySubjectFootprint(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := filepath.Join(dir, "base.duckdb")
	shard := filepath.Join(dir, "shard.duckdb")
	out := filepath.Join(dir, "out.duckdb")
	subjOut := filepath.Join(dir, "subject-index.json")

	// Base stamped with a DIFFERENT (stale) pipeline_version → the gate drops it.
	// It carries a glossary footprint as if series 21 were up to date.
	buildShard(t, base, func(st *store.Store) {
		_ = st.SetMeta("pipeline_version", "stale-pv")
		for _, m := range subjectmeta.All {
			_ = st.SetMeta("subject_fp_"+m.Name, subjectmeta.Footprint(m))
		}
		_ = st.UpsertSpec(model.Spec{SpecID: "21.905", Series: "21", DocType: "TR"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "21.905", Release: "Rel-18", Version: "18.0.0"})
		_ = st.UpsertAcronym(model.Acronym{Term: "XYZ", Expansion: "Thing", SourceSeries: "21"})
	})
	// Narrow delta shard touches ONLY series 24 — neither glossary (21) nor LI (33).
	buildShard(t, shard, func(st *store.Store) {
		_ = st.SetMeta("pipeline_version", model.PipelineVersion(""))
		_ = st.UpsertSpec(model.Spec{SpecID: "24.501", Series: "24", DocType: "TS"})
		_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-19", Version: "19.2.0"})
		_ = st.InsertClauses([]model.Clause{cl(1, "24.501", "Rel-19", "19.2.0", "5.1")})
	})

	if err := run(ctx, out, []string{shard}, false, "", base, false, subjOut, ""); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(subjOut)
	if err != nil {
		t.Fatal(err)
	}
	var idx map[string]string
	if err := json.Unmarshal(b, &idx); err != nil {
		t.Fatal(err)
	}
	// Every subject is unrebuilt this run (only series 24 in the shard), and the
	// base was dropped → every published footprint must be empty so discover heals.
	for _, m := range subjectmeta.All {
		if idx[m.Name] != "" {
			t.Errorf("subject %q published footprint=%q after dropped base; want empty (self-heal)", m.Name, idx[m.Name])
		}
	}
	// The stamped DB meta must agree with the published index (no internal lie).
	st, err := store.OpenReadOnly(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	for _, m := range subjectmeta.All {
		var v string
		_ = st.DB().QueryRowContext(ctx, `SELECT value FROM schema_meta WHERE key=?`, "subject_fp_"+m.Name).Scan(&v)
		if v != "" {
			t.Errorf("DB meta subject_fp_%s=%q after dropped base; want empty", m.Name, v)
		}
	}
}
