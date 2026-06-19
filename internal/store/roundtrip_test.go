package store

import (
	"context"
	"os"
	"testing"
)

// TestPhase0RustGoRoundTrip is the migration keystone: it opens, READ-ONLY, a DuckDB
// file written by the Rust store crate (rust/store roundtrip-fixture) and asserts the
// Go serve side can read the data + the FLOAT[1024] vector back. Green proves Rust
// (duckdb-rs, bundled DuckDB 1.4.x) and Go (go-duckdb, engine 1.4.3) share the on-disk
// storage format — the single risk the whole "Rust writes DuckDB, Go reads it"
// architecture hinges on.
//
// Skipped unless RT_DB points at a fixture the Rust binary produced:
//
//	cargo run --manifest-path rust/store/Cargo.toml --bin roundtrip-fixture -- /tmp/rt.duckdb
//	RT_DB=/tmp/rt.duckdb go test ./internal/store/ -run TestPhase0RustGoRoundTrip -v
func TestPhase0RustGoRoundTrip(t *testing.T) {
	dbPath := os.Getenv("RT_DB")
	if dbPath == "" {
		t.Skip("RT_DB not set — build+run rust/store roundtrip-fixture first (Phase 0 round-trip)")
	}

	s, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("Go cannot open the Rust-written DuckDB read-only: %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	var engine string
	if err := s.DB().QueryRowContext(ctx, "SELECT version()").Scan(&engine); err != nil {
		t.Fatalf("SELECT version() on the Rust-written DB: %v", err)
	}
	t.Logf("engine reported by go-duckdb over the Rust-written file: %s", engine)

	// The data-only fixture seeds 1 spec and 3 embedded clauses (chunk_ids 1..3).
	var specs, clauses, dim int
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM specs").Scan(&specs); err != nil {
		t.Fatalf("read specs: %v", err)
	}
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM clauses WHERE embedding IS NOT NULL").Scan(&clauses); err != nil {
		t.Fatalf("read clauses: %v", err)
	}
	if err := s.DB().QueryRowContext(ctx, "SELECT len(embedding) FROM clauses WHERE chunk_id = 1").Scan(&dim); err != nil {
		t.Fatalf("read vector: %v", err)
	}
	if specs != 1 || clauses != 3 || dim != 1024 {
		t.Fatalf("round-trip mismatch: specs=%d clauses=%d vec_dim=%d (want 1/3/1024)", specs, clauses, dim)
	}
	t.Logf("Rust→Go round-trip OK: specs=%d clauses=%d vec_dim=%d", specs, clauses, dim)
}

// TestPhase2RustGoRoundTripIndexed proves Go can consume the FTS + frozen HNSW + sparse
// that the Rust store crate BUILT (roundtrip-fixture <db> indexes): the serve-side
// LoadVSS/LoadFTS/LoadSparse must accept a Rust-built corpus, and a vector search must
// run over the Rust-built HNSW. This retires the extension/index-format risk on top of
// the raw storage-format one.
//
//	cargo run --manifest-path rust/store/Cargo.toml --bin roundtrip-fixture -- /tmp/rt_full.duckdb indexes
//	RT_DB_FULL=/tmp/rt_full.duckdb go test ./internal/store/ -run TestPhase2RustGoRoundTripIndexed -v
func TestPhase2RustGoRoundTripIndexed(t *testing.T) {
	dbPath := os.Getenv("RT_DB_FULL")
	if dbPath == "" {
		t.Skip("RT_DB_FULL not set — run roundtrip-fixture <db> indexes first")
	}
	s, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("open Rust-built indexed DB: %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	if err := s.LoadFTS(ctx); err != nil || !s.FTSAvailable() {
		t.Fatalf("Go cannot load the Rust-built FTS index: err=%v available=%v", err, s.FTSAvailable())
	}
	if err := s.LoadVSS(ctx); err != nil || !s.VSSAvailable() {
		t.Fatalf("Go cannot load the Rust-built frozen HNSW: err=%v available=%v", err, s.VSSAvailable())
	}
	if err := s.LoadSparse(ctx); err != nil || !s.SparseAvailable() {
		t.Fatalf("Go cannot load the Rust-built sparse postings: err=%v available=%v", err, s.SparseAvailable())
	}

	// A vector search must run over the Rust-built HNSW and return clauses.
	q := make([]float32, 1024)
	for i := range q {
		q[i] = 0.01
	}
	hits, err := s.SearchVectors(ctx, q, SpecFilter{}, 3)
	if err != nil {
		t.Fatalf("vector search over Rust-built HNSW: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("vector search returned no hits over the Rust-built HNSW")
	}
	t.Logf("Rust-built FTS+HNSW+sparse consumed by Go OK: %d vector hits", len(hits))
}
