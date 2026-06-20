package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRustGlossaryE2E proves the glossary subject (Phase 8): the Rust `ingest` binary,
// when it ingests TS 21.905, seeds the acronym vocabulary from the abbreviations region
// into the acronyms table, which the Go serve side reads back (the resolve_term source).
//
// Skipped unless RUST_INGEST points at the built binary.
func TestRustGlossaryE2E(t *testing.T) {
	bin := os.Getenv("RUST_INGEST")
	if bin == "" {
		t.Skip("RUST_INGEST not set — build rust/ingest first")
	}
	ctx := context.Background()
	dir := t.TempDir()

	// 21905-j00 under Rel-19 → spec 21.905, the glossary's owned spec.
	htmlDir := filepath.Join(dir, "convert", "Rel-19")
	if err := os.MkdirAll(htmlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	htmlPath := filepath.Join(htmlDir, "21905-j00.html")
	// The "Abbreviations" leaf carries the entries in its own clause text (the real
	// 21.905 shape the Go glossary expects); the next numbered clause ends the region.
	const doc = `<html><body>
<h1>1 Scope</h1><p>scope</p>
<h1>3.2 Abbreviations</h1><pre>AMF	Access and Mobility Management Function
SMF	Session Management Function</pre>
<h1>4 Vocabulary</h1><p>AAA	must not be captured</p>
</body></html>`
	if err := os.WriteFile(htmlPath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	db := filepath.Join(dir, "g.duckdb")
	out, err := exec.Command(bin, "--html", htmlPath, "--db", db).CombinedOutput()
	if err != nil {
		t.Fatalf("rust ingest (glossary): %v\n%s", err, out)
	}
	t.Logf("ingest: %s", out)

	s, err := OpenReadOnly(db)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s.Close() }()

	var n int
	if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM acronyms").Scan(&n); err != nil {
		t.Fatalf("count acronyms: %v", err)
	}
	var exp, series string
	if err := s.DB().QueryRowContext(ctx, "SELECT expansion, source_series FROM acronyms WHERE term='AMF'").Scan(&exp, &series); err != nil {
		t.Fatalf("read AMF: %v", err)
	}
	if n != 2 || exp != "Access and Mobility Management Function" || series != "21" {
		t.Fatalf("glossary mismatch: n=%d amf=%q series=%q (want 2/AMF expansion/21)", n, exp, series)
	}
	t.Logf("Rust glossary → Go read OK: %d acronyms seeded", n)
}
