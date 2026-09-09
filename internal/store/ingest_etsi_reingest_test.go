package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRustIngestEtsiDoesNotReingestNonUTF8 pins the defect that made the ETSI
// corpus grow by 566 clauses on every single build, on a converted tree that
// never changed.
//
// The resume check read each file with std::fs::read_to_string — STRICT UTF-8,
// Err on anything else — while ingest_etsi_one read the same file through
// parse3gpp::html_bytes::read_html, which falls back to windows-1252. So a
// deliverable that is not valid UTF-8 could be INGESTED but never RECOGNISED as
// already ingested: the read error became None, the `if let` did not match, and
// the file was written again. Every build, for ever.
//
// Measured on the published corpus (2026-09-09): exactly two of 11 822 converted
// files are not UTF-8, and both had been written FIFTEEN times — 1 155 rows for
// 77 clauses, 7 335 rows for 489. 77 + 489 = 566, which is precisely the gap
// between builds D (3 175 274), E (3 175 840) and F (3 176 406).
//
// EVERY GATE STAYED GREEN, including the work-list reconciliation added the same
// day: it asks whether anything is MISSING, and nothing was.
//
// The fixture is genuinely non-UTF-8: 0xE9 is "é" in windows-1252 and an invalid
// UTF-8 sequence on its own, which is exactly the shape pdftotext produces from an
// ETSI PDF with a Latin-1 text layer.
func TestRustIngestEtsiDoesNotReingestNonUTF8(t *testing.T) {
	bin := os.Getenv("RUST_INGEST")
	if bin == "" {
		t.Skip("RUST_INGEST not set — build rust/ingest first")
	}
	ctx := context.Background()
	dir := t.TempDir()
	conv := filepath.Join(dir, "etsi")
	if err := os.MkdirAll(conv, 0o755); err != nil {
		t.Fatal(err)
	}

	// A clean UTF-8 deliverable beside it, so a regression that broke resume for
	// EVERYTHING would be told apart from one that breaks it only for this file.
	if err := os.WriteFile(filepath.Join(conv, "utf8.html"),
		[]byte(`<!-- ETSI-SPEC: 103 221-1 | 1.21.1 --><html><body><h1>1 Scope</h1><p>ascii only</p></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	latin1 := []byte(`<!-- ETSI-SPEC: 104 066 | 1.1.1 --><html><body><h1>1 Port` + "\xe9" + `e</h1><p>caf` + "\xe9" + `</p><h1>2 Deux</h1><p>x</p></body></html>`)
	if err := os.WriteFile(filepath.Join(conv, "cp1252.html"), latin1, 0o644); err != nil {
		t.Fatal(err)
	}

	db := filepath.Join(dir, "etsi.duckdb")
	run := func(pass string) {
		out, err := exec.Command(bin, "--etsi", "--convert", conv, "--db", db, "--resume").CombinedOutput()
		if err != nil {
			t.Fatalf("rust ingest --etsi (%s): %v\n%s", pass, err, out)
		}
		t.Logf("ingest %s: %s", pass, out)
	}
	count := func() int {
		s, err := OpenReadOnly(db)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		defer func() { _ = s.Close() }()
		var n int
		if err := s.DB().QueryRowContext(ctx, "SELECT count(*) FROM clauses").Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	run("first")
	first := count()
	if first == 0 {
		t.Fatal("the first ingest wrote nothing; the fixture never reached the parser")
	}

	run("second")
	second := count()
	if second != first {
		t.Errorf("a second ingest of an UNCHANGED tree grew the corpus from %d to %d clause(s) "+
			"(+%d). The resume check and the ingest must read a file the same way, or the resume "+
			"key is not the ingest key.", first, second, second-first)
	}

	// And name the offender, so a failure says WHICH deliverable came back rather
	// than only that the total moved.
	s, err := OpenReadOnly(db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	rows, err := s.DB().QueryContext(ctx, `
		SELECT spec_id, version, min(c), sum(c) FROM (
			SELECT spec_id, version, clause_path, heading, text, count(*) AS c
			FROM clauses GROUP BY 1,2,3,4,5
		) GROUP BY 1,2 HAVING min(c) > 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, ver string
		var minC, total int
		_ = rows.Scan(&id, &ver, &minC, &total)
		t.Errorf("%s %s: every distinct clause row appears %d times (%d rows) — the whole "+
			"deliverable was written more than once", id, ver, minC, total)
	}
}
