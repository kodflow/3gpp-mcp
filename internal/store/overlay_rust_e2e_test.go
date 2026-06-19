package store

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRustOverlayE2E proves the Rust overlay (store-rs port of cmd/overlay): a real vector
// injected into a shard (via the Rust embed-io import) is fused onto a separate lexical base
// by NATURAL identity (spec_id,release,clause_path,text), the embedding_model is stamped, and
// the Go serve side reads the vectorised base back.
//
// Skipped unless RUST_OVERLAY + RUST_EMBED_IO + RUST_INGEST point at the built binaries.
func TestRustOverlayE2E(t *testing.T) {
	ovBin, ioBin, ingBin := os.Getenv("RUST_OVERLAY"), os.Getenv("RUST_EMBED_IO"), os.Getenv("RUST_INGEST")
	if ovBin == "" || ioBin == "" || ingBin == "" {
		t.Skip("RUST_OVERLAY / RUST_EMBED_IO / RUST_INGEST not set")
	}
	ctx := context.Background()
	dir := t.TempDir()
	conv := filepath.Join(dir, "convert", "Rel-19")
	if err := os.MkdirAll(conv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conv, "23501-j50.html"),
		[]byte(`<html><body><h1>1 Scope</h1><p>the scope clause text</p></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun := func(name string, args ...string) {
		t.Helper()
		if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}

	// Base (lexical) + an identical shard to carry the vector.
	base := filepath.Join(dir, "base.duckdb")
	shard := filepath.Join(dir, "shard.duckdb")
	mustRun(ingBin, "--series", "23", "--convert", filepath.Join(dir, "convert"), "--db", base)
	mustRun(ingBin, "--series", "23", "--convert", filepath.Join(dir, "convert"), "--db", shard)

	// Inject a real 1024-dim vector into the shard's clause chunk_id=1 via the Rust embed-io.
	vec := make([]string, 1024)
	for i := range vec {
		vec[i] = "0.1"
	}
	vjsonl := filepath.Join(dir, "vecs.jsonl")
	if err := os.WriteFile(vjsonl,
		[]byte(fmt.Sprintf(`{"chunk_id":1,"hash":"h1","vec":[%s]}`+"\n", strings.Join(vec, ","))), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(ioBin, "--db", shard, "--import-vectors", vjsonl, "--embed-identity", "bge-m3")

	// Overlay the shard's vector onto the lexical base by natural identity.
	mustRun(ovBin, "--base", base, "--vec", shard, "--embedding-model", "bge-m3")

	s, err := OpenReadOnly(base)
	if err != nil {
		t.Fatalf("reopen base: %v", err)
	}
	defer func() { _ = s.Close() }()
	var embedded int
	var model string
	_ = s.DB().QueryRowContext(ctx, "SELECT count(*) FROM clauses WHERE embedding IS NOT NULL").Scan(&embedded)
	_ = s.DB().QueryRowContext(ctx, "SELECT value FROM schema_meta WHERE key='embedding_model'").Scan(&model)
	if embedded < 1 || model != "bge-m3" {
		t.Fatalf("overlay mismatch: embedded=%d model=%q (want >=1 vectorised, model bge-m3)", embedded, model)
	}
	t.Logf("Rust overlay → Go read OK: %d clause(s) vectorised, model=%s", embedded, model)
}
