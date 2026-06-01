package openapi

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
	"github.com/kodflow/3gpp-mcp/internal/store"
)

// seedAPI inserts one operation per release so the tests can assert which
// releases survive a (degraded or scoped) ingest. op_ids start high (1000+) so
// they don't collide with the per-run op_id counter a real ingest assigns from 1
// (op_id is the api_operations PK; a collision would make INSERT OR REPLACE
// overwrite an inherited row, which is a test artifact, not the behaviour under
// test — the scoped-delete contract is keyed on `release`).
func seedAPI(t *testing.T, db *store.Store, releases ...string) {
	t.Helper()
	ops := make([]model.APIOperation, len(releases))
	for i, rel := range releases {
		ops[i] = model.APIOperation{
			OpID: uint64(1000 + i), SpecID: "29.518", Release: rel, Version: "18.0.0",
			Service: "namf-comm", Path: "/x", Method: "GET", OperationID: "Op" + rel,
		}
	}
	if err := db.InsertAPIOperations(ops); err != nil {
		t.Fatalf("seed api: %v", err)
	}
}

func apiCount(t *testing.T, ctx context.Context, db *store.Store, release string) int {
	t.Helper()
	q := `SELECT count(*) FROM api_operations`
	var args []any
	if release != "" {
		q += ` WHERE release = ?`
		args = append(args, release)
	}
	var n int
	if err := db.DB().QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		t.Fatalf("count api: %v", err)
	}
	return n
}

// TestDegradedFetchPreservesInheritedRows is the PR-7 lock for
// openapi-clearapi-full-recompute-and-loss: when the source dir is empty/missing
// (a degraded Forge fetch the workflow tolerates), IngestDir must NOT wipe the
// inherited api_* rows — it must leave them exactly as they were. Before the fix
// IngestDir called ClearAPI unconditionally and a degraded run published an empty
// API channel.
func TestDegradedFetchPreservesInheritedRows(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.duckdb")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Reset(ctx); err != nil {
		t.Fatal(err)
	}

	seedAPI(t, db, "Rel-17", "Rel-18", "Rel-19")
	if got := apiCount(t, ctx, db, ""); got != 3 {
		t.Fatalf("seed precondition: %d api rows, want 3", got)
	}

	// Case 1: source dir does not exist at all (total fetch failure).
	st, err := IngestDir(ctx, db, filepath.Join(dir, "does-not-exist"), nil, nil)
	if err != nil {
		t.Fatalf("IngestDir(missing src): %v", err)
	}
	if st.Releases != 0 || st.Operations != 0 {
		t.Errorf("degraded ingest reported work: %+v", st)
	}
	if got := apiCount(t, ctx, db, ""); got != 3 {
		t.Fatalf("missing-src degraded fetch wiped rows: %d left, want 3 inherited", got)
	}

	// Case 2: source dir exists but is empty of usable content (Rel-* dirs with no
	// <sha>/*.yaml — the partial-degraded case).
	empty := filepath.Join(dir, "5g-apis")
	if err := os.MkdirAll(filepath.Join(empty, "Rel-18", "deadbeef"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := IngestDir(ctx, db, empty, nil, nil); err != nil {
		t.Fatalf("IngestDir(empty src): %v", err)
	}
	if got := apiCount(t, ctx, db, ""); got != 3 {
		t.Fatalf("empty-src degraded fetch wiped rows: %d left, want 3 inherited", got)
	}
}

// TestScopedDeletePreservesOtherReleases locks the scoped-delete half: ingesting
// ONLY Rel-19 (the only release with YAML on disk) must replace just Rel-19's
// rows and leave Rel-17/Rel-18 inherited rows intact — the blanket ClearAPI used
// to drop them all.
func TestScopedDeletePreservesOtherReleases(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.duckdb")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Reset(ctx); err != nil {
		t.Fatal(err)
	}

	seedAPI(t, db, "Rel-17", "Rel-18", "Rel-19")

	// A real Rel-19 source: copy the parse fixture under Rel-19/<sha>/.
	src := filepath.Join(dir, "5g-apis")
	shaDir := filepath.Join(src, "Rel-19", "cafef00d")
	if err := os.MkdirAll(shaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "TS29518_Namf_Communication.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shaDir, "namf.yaml"), fixture, 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := IngestDir(ctx, db, src, nil, nil)
	if err != nil {
		t.Fatalf("IngestDir(Rel-19): %v", err)
	}
	if st.Releases != 1 || st.Operations == 0 {
		t.Errorf("expected Rel-19 ingest to do work, got %+v", st)
	}

	// Rel-17 and Rel-18 inherited rows must survive (scoped delete touched only 19).
	if got := apiCount(t, ctx, db, "Rel-17"); got != 1 {
		t.Errorf("Rel-17 rows = %d, want 1 (scoped delete must not touch siblings)", got)
	}
	if got := apiCount(t, ctx, db, "Rel-18"); got != 1 {
		t.Errorf("Rel-18 rows = %d, want 1 (scoped delete must not touch siblings)", got)
	}
	// Rel-19 was replaced by the fixture's operations (the seeded single row is gone,
	// the parsed ones are present).
	if got := apiCount(t, ctx, db, "Rel-19"); got < 1 {
		t.Errorf("Rel-19 rows = %d, want the re-ingested fixture operations", got)
	}
}
