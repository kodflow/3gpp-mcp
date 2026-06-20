package store

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRustMergeE2E proves the Rust merge (store-rs port of cmd/merge): it folds two
// Rust-ingested shards into one corpus DB the Go serve side reads back, and emits the three
// index sidecars discover consumes (corpus-index / subject-index / build-index) with the
// SAME identity digests the Go pipeline computes — so the two can never drift the
// incremental-build decision.
//
// Skipped unless RUST_MERGE + RUST_INGEST point at the built binaries.
func TestRustMergeE2E(t *testing.T) {
	mergeBin, ingBin := os.Getenv("RUST_MERGE"), os.Getenv("RUST_INGEST")
	if mergeBin == "" || ingBin == "" {
		t.Skip("RUST_MERGE / RUST_INGEST not set — build rust/store + rust/ingest first")
	}
	ctx := context.Background()
	dir := t.TempDir()
	conv := filepath.Join(dir, "convert", "Rel-19")
	if err := os.MkdirAll(conv, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(conv, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("23501-j50.html", `<html><body><h1>1 Scope</h1><p>a</p><h1>2 AMF</h1><p>b</p></body></html>`)
	write("29518-j00.html", `<html><body><h1>1 Scope</h1><p>c</p></body></html>`)

	// Two single-series shards.
	shard23, shard29 := filepath.Join(dir, "s23.duckdb"), filepath.Join(dir, "s29.duckdb")
	for _, s := range [][2]string{{"23", shard23}, {"29", shard29}} {
		if out, err := exec.Command(ingBin, "--series", s[0], "--convert", filepath.Join(dir, "convert"), "--db", s[1]).CombinedOutput(); err != nil {
			t.Fatalf("ingest series %s: %v\n%s", s[0], err, out)
		}
	}

	merged := filepath.Join(dir, "merged.duckdb")
	corpusIdx := filepath.Join(dir, "corpus-index.json")
	out, err := exec.Command(mergeBin, "--out", merged, "--no-hnsw",
		"--index-out", corpusIdx,
		"--subject-index-out", filepath.Join(dir, "subject-index.json"),
		"--build-index-out", filepath.Join(dir, "build-index.json"),
		shard23, shard29).CombinedOutput()
	if err != nil {
		t.Fatalf("rust merge: %v\n%s", err, out)
	}
	t.Logf("merge: %s", out)

	// The merged DB opens read-only on the Go side and carries both shards' specs.
	s, err := OpenReadOnly(merged)
	if err != nil {
		t.Fatalf("reopen merged: %v", err)
	}
	defer func() { _ = s.Close() }()
	var specs, distinct int
	_ = s.DB().QueryRowContext(ctx, "SELECT count(*) FROM specs").Scan(&specs)
	_ = s.DB().QueryRowContext(ctx, "SELECT count(DISTINCT chunk_id) FROM clauses").Scan(&distinct)
	var clauses int
	_ = s.DB().QueryRowContext(ctx, "SELECT count(*) FROM clauses").Scan(&clauses)
	if specs != 2 || distinct != clauses {
		t.Fatalf("merged mismatch: specs=%d distinct=%d clauses=%d (want 2 specs, distinct==clauses)", specs, distinct, clauses)
	}

	// corpus-index lists both specs at their version.
	b, err := os.ReadFile(corpusIdx)
	if err != nil {
		t.Fatalf("read corpus-index: %v", err)
	}
	var idx map[string]string
	if err := json.Unmarshal(b, &idx); err != nil {
		t.Fatalf("parse corpus-index: %v", err)
	}
	if idx["23.501|Rel-19"] == "" || idx["29.518|Rel-19"] == "" {
		t.Fatalf("corpus-index missing specs: %v", idx)
	}

	// build-index carries the three canonical identities (non-empty 12-hex digests).
	bb, err := os.ReadFile(filepath.Join(dir, "build-index.json"))
	if err != nil {
		t.Fatalf("read build-index: %v", err)
	}
	var bi map[string]string
	if err := json.Unmarshal(bb, &bi); err != nil {
		t.Fatalf("parse build-index: %v", err)
	}
	for _, k := range []string{"spec_ingest_identity", "global_enrichment_identity", "embed_identity"} {
		if len(bi[k]) != 12 {
			t.Fatalf("build-index %s = %q (want 12-hex digest)", k, bi[k])
		}
	}
	t.Logf("Rust merge → Go read OK: %d specs, sidecars valid, build-index=%v", specs, bi)
}
