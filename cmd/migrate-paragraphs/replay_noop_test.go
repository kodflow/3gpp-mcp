package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// runAsMainEnv turns the test binary into the command itself. The re-run below
// has to go through main() — the flag handling, the vss load, BoundMemory, the
// order of build/attest/report, and the close — because the property it pins is
// about what the PROCESS leaves on disk, and a test that called build() and
// CheckParagraphAttestation() by hand would be checking its own copy of that
// sequence.
const runAsMainEnv = "MIGRATE_PARAGRAPHS_TEST_RUN_AS_MAIN"

func TestMain(m *testing.M) {
	if raw, ok := os.LookupEnv(runAsMainEnv); ok {
		os.Args = append([]string{os.Args[0]}, strings.Split(raw, "\x1f")...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runMain executes the command in a child process, exactly as the pipeline
// launches it, and fails the test on a non-zero exit.
func runMain(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), runAsMainEnv+"="+strings.Join(args, "\x1f"))
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if err := cmd.Run(); err != nil {
		t.Fatalf("migrate-paragraphs %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, o.String(), e.String())
	}
	return o.String(), e.String()
}

// fileIdentity is what decides whether the image re-pushes the corpus layer:
// the bytes, and the mtime the goal runner records for a file this size.
func fileIdentity(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d bytes sha256=%s mtime=%d", st.Size(), hex.EncodeToString(h.Sum(nil)), st.ModTime().UnixNano())
}

// TestAReplayOfAConvertedCorpusWritesNothing pins the property the price of
// replaying `paragraphs` rests on.
//
// The pipeline passes --drop-clauses unconditionally, so every build in which
// enrich (or embed) publishes a new provenance replays this command on a corpus
// it already converted — and so does any change to the step's fingerprint, such
// as the ExcludeTests that stopped main_test.go counting. Whether that replay
// costs 0.2 s or a 22 GB image layer depends on one thing: whether the process
// leaves the file as it found it. Measured on 2026-09-11 against copies of both
// shipped corpora, it does (sha256 and mtime identical after the Run and after
// the step's Validate). This keeps it true: a later "harmless" re-stamp of the
// attestation, or a CHECKPOINT on the already-converted path, would pass every
// other test here and move 42 GB per build.
func TestAReplayOfAConvertedCorpusWritesNothing(t *testing.T) {
	h := fixture(t)
	var path string
	if err := h.QueryRow(`SELECT path FROM duckdb_databases() WHERE database_name = current_database()`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	// The child needs the file: DuckDB holds it exclusively while it is open.
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}

	// The first conversion, through the same entry point, so the corpus under
	// test is one this command produced and attested rather than a hand-built
	// imitation of one.
	_, first := runMain(t, "--db", path, "--drop-clauses")
	if !strings.Contains(first, "attested") || !strings.Contains(first, "is now a view") {
		t.Fatalf("the first run did not convert and attest the fixture:\n%s", first)
	}

	before := fileIdentity(t, path)
	for _, args := range [][]string{
		{"--db", path, "--drop-clauses"}, // the step's Run
		{"--db", path, "--attested"},     // the step's Validate, on every plan
	} {
		out, errOut := runMain(t, args...)
		if !strings.Contains(out, "clause_occ=8") {
			t.Errorf("%v reported %q, want the 8 occurrences of the fixture", args, out)
		}
		// The already-converted path re-verifies and re-stamps only when the
		// attestation is missing or stale; a fresh one must be honoured, or the
		// replay is a full verification (7m12 on 3GPP) and a write.
		if strings.Contains(errOut, "verifying once") {
			t.Errorf("%v re-verified a corpus whose attestation is current:\n%s", args, errOut)
		}
		if after := fileIdentity(t, path); after != before {
			t.Errorf("%v rewrote an already-converted corpus:\n  before %s\n  after  %s", args, before, after)
		}
		if _, err := os.Stat(path + ".wal"); err == nil {
			t.Errorf("%v left a write-ahead log beside the corpus: the next open replays it into the file", args)
		}
	}
}
