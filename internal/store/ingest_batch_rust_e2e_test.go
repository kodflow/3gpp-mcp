package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRustIngestBatchE2E proves the Rust ingest SERIES BATCH mode (the drop-in for Go
// `ingest --series NN --out … --resume`, Phase 10 core repoint): it walks a converted
// corpus, ingests every spec of the series with globally-distinct chunk_ids, skips
// out-of-series specs, and the Go serve side reads the multi-spec shard back.
//
// Skipped unless RUST_INGEST points at the built binary.
func TestRustIngestBatchE2E(t *testing.T) {
	bin := os.Getenv("RUST_INGEST")
	if bin == "" {
		t.Skip("RUST_INGEST not set — build rust/ingest first")
	}
	ctx := context.Background()
	dir := t.TempDir()
	conv := filepath.Join(dir, "convert", "Rel-19")
	if err := os.MkdirAll(conv, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two series-23 specs + one series-29 spec (must be excluded by --series 23).
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(conv, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("23501-j50.html", `<html><body><h1>1 Scope</h1><p>a</p><h1>2 AMF</h1><p>b</p></body></html>`)
	write("23502-j00.html", `<html><body><h1>1 Scope</h1><p>c</p></body></html>`)
	write("29518-j00.html", `<html><body><h1>1 Scope</h1><p>z</p></body></html>`)

	db := filepath.Join(dir, "shard.duckdb")
	run := func(label string) {
		t.Helper()
		out, err := exec.Command(bin, "--series", "23", "--convert", filepath.Join(dir, "convert"), "--db", db, "--resume").CombinedOutput()
		if err != nil {
			t.Fatalf("rust ingest batch (%s): %v\n%s", label, err, out)
		}
		t.Logf("ingest batch (%s): %s", label, out)
	}
	// Run twice WITHOUT a reader open in between (DuckDB rw conflicts with a held RO conn):
	// the second --resume run must skip every already-'done' spec.
	run("first")
	run("resume")

	s, err := OpenReadOnly(db)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s.Close() }()

	var specs, clauses, distinct, series29 int
	_ = s.DB().QueryRowContext(ctx, "SELECT count(*) FROM specs").Scan(&specs)
	_ = s.DB().QueryRowContext(ctx, "SELECT count(*) FROM clauses").Scan(&clauses)
	_ = s.DB().QueryRowContext(ctx, "SELECT count(DISTINCT chunk_id) FROM clauses").Scan(&distinct)
	_ = s.DB().QueryRowContext(ctx, "SELECT count(*) FROM specs WHERE series='29'").Scan(&series29)
	// 2 series-23 specs, 3 clauses with distinct chunk_ids, no series-29 spect, and the
	// resume run left the count unchanged (idempotent).
	if specs != 2 || clauses != 3 || distinct != 3 || series29 != 0 {
		t.Fatalf("batch mismatch: specs=%d clauses=%d distinct=%d series29=%d (want 2/3/3/0)", specs, clauses, distinct, series29)
	}
	t.Logf("Rust series-batch ingest → Go read OK: %d specs, %d clauses, resume idempotent", specs, clauses)
}
