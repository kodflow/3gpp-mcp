package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestIngestLogResumeContract walks the full resume state machine on the Store
// layer (no ingest pipeline): a 'started' row is reported as not-done, the
// 'done' flip makes it skip-eligible, a pipeline_version mismatch invalidates
// the row, and ResetIngestLog wipes only the stale entries.
func TestIngestLogResumeContract(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "shard.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}

	pv := "v1"
	// 1. Started but not done → IsIngestDone false.
	if err := st.MarkIngestStarted(ctx, "24.501", "18.0.0", pv); err != nil {
		t.Fatal(err)
	}
	done, err := st.IsIngestDone(ctx, "24.501", "18.0.0", pv)
	if err != nil || done {
		t.Fatalf("started row reported as done (err=%v)", err)
	}
	// 2. Flip to done → IsIngestDone true.
	if err := st.MarkIngestDone(ctx, "24.501", "18.0.0"); err != nil {
		t.Fatal(err)
	}
	done, err = st.IsIngestDone(ctx, "24.501", "18.0.0", pv)
	if err != nil || !done {
		t.Fatalf("done row reported as not done (err=%v)", err)
	}
	// 3. Different pipeline version → not done (stale).
	done, _ = st.IsIngestDone(ctx, "24.501", "18.0.0", "v2")
	if done {
		t.Fatal("stale-pipeline row should not count as done")
	}
	// 4. IngestProgress counts under the current pipeline only.
	if err := st.MarkIngestStarted(ctx, "23.501", "19.0.0", pv); err != nil {
		t.Fatal(err)
	}
	doneN, startedN, err := st.IngestProgress(ctx, pv)
	if err != nil {
		t.Fatal(err)
	}
	if doneN != 1 || startedN != 1 {
		t.Errorf("IngestProgress = (done=%d, started=%d), want (1, 1)", doneN, startedN)
	}
	// 5. ResetIngestLog with a new pipeline_version wipes everything from pv.
	if err := st.ResetIngestLog(ctx, "v2"); err != nil {
		t.Fatal(err)
	}
	doneN, startedN, _ = st.IngestProgress(ctx, pv)
	if doneN != 0 || startedN != 0 {
		t.Errorf("after Reset(v2), counts for v1 = (done=%d, started=%d), want (0,0)", doneN, startedN)
	}
}

// TestPurgeSpecScope verifies the half-ingested cleanup contract: clauses,
// spec_versions, and the ingest_log row scoped to (spec, version) are gone;
// the parent `specs` row is intentionally KEPT (shared across versions —
// the re-ingest UPSERTs it).
func TestPurgeSpecScope(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "shard.duckdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.Reset(ctx); err != nil {
		t.Fatal(err)
	}

	pv := "v1"
	_ = st.UpsertSpec(model.Spec{SpecID: "24.501", Series: "24", DocType: "TS"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-18", Version: "18.0.0"})
	_ = st.UpsertVersion(model.SpecVersion{SpecID: "24.501", Release: "Rel-19", Version: "19.0.0"})
	_ = st.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "24.501", Release: "Rel-18", Version: "18.0.0", ClausePath: "1", Heading: "h", Text: "t"},
		{ChunkID: 2, SpecID: "24.501", Release: "Rel-19", Version: "19.0.0", ClausePath: "1", Heading: "h", Text: "t"},
	})
	_ = st.MarkIngestStarted(ctx, "24.501", "18.0.0", pv)
	_ = st.MarkIngestDone(ctx, "24.501", "19.0.0")

	if err := st.PurgeSpecScope(ctx, "24.501", "18.0.0"); err != nil {
		t.Fatal(err)
	}

	// 18.0.0 gone, 19.0.0 intact.
	var n18, n19 int
	_ = st.db.QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE spec_id='24.501' AND version='18.0.0'`).Scan(&n18)
	_ = st.db.QueryRowContext(ctx, `SELECT count(*) FROM clauses WHERE spec_id='24.501' AND version='19.0.0'`).Scan(&n19)
	if n18 != 0 || n19 != 1 {
		t.Errorf("clauses after purge: 18=%d, 19=%d (want 0, 1)", n18, n19)
	}
	// ingest_log for 18.0.0 gone.
	done18, _ := st.IsIngestDone(ctx, "24.501", "18.0.0", pv)
	if done18 {
		t.Error("purged ingest_log row still reports done")
	}
	// `specs` row preserved (shared across versions — re-ingest UPSERTs it).
	var sn int
	_ = st.db.QueryRowContext(ctx, `SELECT count(*) FROM specs WHERE spec_id='24.501'`).Scan(&sn)
	if sn != 1 {
		t.Errorf("parent specs row should survive purge, count=%d", sn)
	}
	// MaxChunkID survives the purge (it's a MAX over what's left — chunk 2 still exists).
	maxID, err := st.MaxChunkID(ctx)
	if err != nil || maxID != 2 {
		t.Errorf("MaxChunkID after purge = %d (want 2)", maxID)
	}
}
