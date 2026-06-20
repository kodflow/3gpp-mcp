package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRustIngestEtsiE2E proves the Rust ingest --etsi mode (the Go cmd/ingest --etsi
// drop-in): it walks every .html under --convert, derives id/version from the in-body
// ETSI provenance header (not a 3GPP filename), skips header-less files, and the Go serve
// side reads the ETSI spec back (series/release "ETSI").
//
// Skipped unless RUST_INGEST points at the built binary.
func TestRustIngestEtsiE2E(t *testing.T) {
	bin := os.Getenv("RUST_INGEST")
	if bin == "" {
		t.Skip("RUST_INGEST not set — build rust/ingest first")
	}
	ctx := context.Background()
	dir := t.TempDir()
	conv := filepath.Join(dir, "etsi", "sub")
	if err := os.MkdirAll(conv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conv, "spec.html"),
		[]byte(`<!-- ETSI-SPEC: 103 221-1 | 1.21.1 --><html><body><h1>1 Scope</h1><p>etsi</p><h1>2 X</h1><p>y</p></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conv, "noheader.html"),
		[]byte(`<html><body><h1>1 not etsi</h1></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}

	db := filepath.Join(dir, "etsi.duckdb")
	out, err := exec.Command(bin, "--etsi", "--convert", filepath.Join(dir, "etsi"), "--db", db, "--resume").CombinedOutput()
	if err != nil {
		t.Fatalf("rust ingest --etsi: %v\n%s", err, out)
	}
	t.Logf("ingest --etsi: %s", out)

	s, err := OpenReadOnly(db)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s.Close() }()

	var specs, clauses int
	var specID, series, version string
	_ = s.DB().QueryRowContext(ctx, "SELECT count(*) FROM specs").Scan(&specs)
	_ = s.DB().QueryRowContext(ctx, "SELECT count(*) FROM clauses").Scan(&clauses)
	if err := s.DB().QueryRowContext(ctx, "SELECT spec_id, series FROM specs LIMIT 1").Scan(&specID, &series); err != nil {
		t.Fatalf("read spec: %v", err)
	}
	if err := s.DB().QueryRowContext(ctx, "SELECT version FROM spec_versions LIMIT 1").Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if specs != 1 || clauses != 2 || specID != "ETSI TS 103 221-1" || series != "ETSI" || version != "1.21.1" {
		t.Fatalf("etsi mismatch: specs=%d clauses=%d spec=%q series=%q ver=%q (want 1/2/'ETSI TS 103 221-1'/ETSI/1.21.1)", specs, clauses, specID, series, version)
	}
	t.Logf("Rust --etsi ingest → Go read OK: %s %s, %d clauses", specID, version, clauses)
}
