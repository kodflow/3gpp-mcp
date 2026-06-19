package store

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// TestRustEmbedIoE2E proves the migration's embed I/O path end-to-end: Go ingest
// CREATES the DuckDB (go-duckdb) and inserts vector-less clauses, the RUST embed-io
// (store-rs over duckdb-rs) reads the work-list out of it, writes vectors + builds the
// frozen HNSW INTO the same Go-created file, and Go reads it back read-only with a
// working vector search. This exercises bidirectional Go↔Rust DuckDB writes + the whole
// export→import→index path in Rust.
//
// Skipped unless RUST_EMBED_IO points at the built binary:
//
//	cargo build --manifest-path rust/store/Cargo.toml --bin embed-io
//	RUST_EMBED_IO=rust/store/target/debug/embed-io go test ./internal/store/ -run TestRustEmbedIoE2E -v
func TestRustEmbedIoE2E(t *testing.T) {
	bin := os.Getenv("RUST_EMBED_IO")
	if bin == "" {
		t.Skip("RUST_EMBED_IO not set — build rust/store embed-io first")
	}
	ctx := context.Background()
	dir := t.TempDir()
	db := filepath.Join(dir, "corpus.duckdb")

	// 1. Go ingest creates the DB + inserts vector-less clauses (one with empty text →
	//    not embeddable, so it must NOT appear in the work-list).
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSpec(model.Spec{SpecID: "23.501", Series: "23", DocType: "TS"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertVersion(model.SpecVersion{SpecID: "23.501", Release: "Rel-19", Version: "19.0.0"}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertClauses([]model.Clause{
		{ChunkID: 1, SpecID: "23.501", Release: "Rel-19", Version: "19.0.0", ClausePath: "5.1", Heading: "5.1", Text: "the 5G system architecture"},
		{ChunkID: 2, SpecID: "23.501", Release: "Rel-19", Version: "19.0.0", ClausePath: "5.2", Heading: "5.2", Text: "access and mobility management function"},
		{ChunkID: 3, SpecID: "23.501", Release: "Rel-19", Version: "19.0.0", ClausePath: "5.3", Heading: "5.3", Text: ""},
	}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command(bin, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("rust embed-io %v: %v\n%s", args, err, out)
		}
		t.Logf("embed-io %v: %s", args, out)
	}

	// 2. Rust embed-io exports the work-list (must be exactly the 2 embeddable clauses).
	wl := filepath.Join(dir, "work.jsonl")
	run("--db", db, "--export-worklist", wl)
	ids := readWorklistIDs(t, wl)
	if len(ids) != 2 {
		t.Fatalf("work-list has %d clauses, want 2 (the empty-text clause must be skipped)", len(ids))
	}

	// 3. Generate a 1024-dim vector per work-list clause and import via Rust embed-io,
	//    building the HNSW (this is the final pass — every embeddable clause gets a vector).
	vecs := filepath.Join(dir, "vecs.jsonl")
	writeVecsJSONL(t, vecs, ids)
	run("--db", db, "--import-vectors", vecs, "--embed-identity", "rust-e2e-id", "--build-hnsw")

	// 4. Go reads the Rust-written corpus back: all embeddable clauses embedded, HNSW
	//    loadable, vector search works.
	s2, err := OpenReadOnly(db)
	if err != nil {
		t.Fatalf("reopen Rust-written DB: %v", err)
	}
	defer func() { _ = s2.Close() }()
	if n, err := s2.CountNullEmbeddings(ctx); err != nil || n != 0 {
		t.Fatalf("embeddable-NULL after Rust import = %d (err %v), want 0", n, err)
	}
	if err := s2.LoadVSS(ctx); err != nil || !s2.VSSAvailable() {
		t.Fatalf("Go cannot load the Rust-built HNSW: err=%v available=%v", err, s2.VSSAvailable())
	}
	q := make([]float32, 1024)
	for i := range q {
		q[i] = 0.02
	}
	hits, err := s2.SearchVectors(ctx, q, SpecFilter{}, 2)
	if err != nil || len(hits) == 0 {
		t.Fatalf("vector search over Rust-built HNSW: err=%v hits=%d", err, len(hits))
	}
	if got := s2.GetMeta(ctx, "embedding_model"); got != "rust-e2e-id" {
		t.Fatalf("embedding_model = %q, want rust-e2e-id", got)
	}
	t.Logf("Go→Rust embed-io→Go E2E OK: %d hits, model stamped", len(hits))
}

func readWorklistIDs(t *testing.T, path string) []uint64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var ids []uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var r struct {
			ChunkID uint64 `json:"chunk_id"`
		}
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("parse worklist line: %v", err)
		}
		ids = append(ids, r.ChunkID)
	}
	return ids
}

func writeVecsJSONL(t *testing.T, path string, ids []uint64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	for k, id := range ids {
		vec := make([]float32, 1024)
		for i := range vec {
			vec[i] = float32((i+k*13)%97) / 97.0
		}
		if err := enc.Encode(struct {
			ChunkID uint64    `json:"chunk_id"`
			Hash    string    `json:"hash"`
			Vec     []float32 `json:"vec"`
		}{id, "h", vec}); err != nil {
			t.Fatal(err)
		}
	}
}
