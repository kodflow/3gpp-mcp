package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRustIngestE2E proves the Rust ingest path end-to-end (Phase 5): the Rust `ingest`
// binary parses a converted spec (parse3gpp filename meta + HTML clause walker) and writes
// it to a DuckDB via store-rs, then the Go serve side opens that DuckDB read-only and reads
// the spec, version, and clauses back. This closes the loop "Rust parses + writes a corpus
// the Go MCP serves" with no Go on the write side.
//
// Skipped unless RUST_INGEST points at the built binary:
//
//	cargo build --manifest-path rust/ingest/Cargo.toml --bin ingest
//	RUST_INGEST=rust/ingest/target/debug/ingest go test ./internal/store/ -run TestRustIngestE2E -v
func TestRustIngestE2E(t *testing.T) {
	bin := os.Getenv("RUST_INGEST")
	if bin == "" {
		t.Skip("RUST_INGEST not set — build rust/ingest first")
	}
	ctx := context.Background()
	dir := t.TempDir()

	// A converted spec at …/convert/<Rel>/<num>-<code>.html so the filename derivation
	// yields 23.501 / Rel-19 / 19.5.0 (j50). One numbered clause + an informative annex.
	htmlDir := filepath.Join(dir, "convert", "Rel-19")
	if err := os.MkdirAll(htmlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	htmlPath := filepath.Join(htmlDir, "23501-j50.html")
	const doc = `<html><body>
<h1>1 Scope</h1><p>The present document specifies the 5G system architecture.</p>
<h2>4.2 AMF</h2><p>Access and Mobility Management Function.</p>
<h1>Annex A (informative): Deployment</h1><p>Deployment guidance.</p>
</body></html>`
	if err := os.WriteFile(htmlPath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	db := filepath.Join(dir, "out.duckdb")
	out, err := exec.Command(bin, "--html", htmlPath, "--db", db).CombinedOutput()
	if err != nil {
		t.Fatalf("rust ingest: %v\n%s", err, out)
	}
	t.Logf("ingest: %s", out)

	s, err := OpenReadOnly(db)
	if err != nil {
		t.Fatalf("open Rust-ingested DB: %v", err)
	}
	defer func() { _ = s.Close() }()

	var specID, docType, release, version string
	if err := s.DB().QueryRowContext(ctx, "SELECT spec_id, doc_type FROM specs").Scan(&specID, &docType); err != nil {
		t.Fatalf("read spec: %v", err)
	}
	if err := s.DB().QueryRowContext(ctx, "SELECT release, version FROM spec_versions").Scan(&release, &version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if specID != "23.501" || docType != "TS" || release != "Rel-19" || version != "19.5.0" {
		t.Fatalf("catalogue mismatch: spec=%s type=%s rel=%s ver=%s (want 23.501/TS/Rel-19/19.5.0)", specID, docType, release, version)
	}

	var clauses, annexNonNorm int
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM clauses").Scan(&clauses); err != nil {
		t.Fatalf("count clauses: %v", err)
	}
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM clauses WHERE clause_path = 'Annex A' AND is_normative = false").Scan(&annexNonNorm); err != nil {
		t.Fatalf("read annex: %v", err)
	}
	if clauses != 3 || annexNonNorm != 1 {
		t.Fatalf("clauses=%d informative-annex=%d (want 3/1)", clauses, annexNonNorm)
	}

	// resume ledger marked done for this (spec, version).
	var status string
	if err := s.DB().QueryRowContext(ctx, "SELECT status FROM ingest_log WHERE spec_id='23.501'").Scan(&status); err != nil {
		t.Fatalf("read ingest_log: %v", err)
	}
	if status != "done" {
		t.Fatalf("ingest_log status = %q, want done", status)
	}
	t.Logf("Rust ingest → Go read OK: 23.501 Rel-19 19.5.0, %d clauses, ledger done", clauses)
}
