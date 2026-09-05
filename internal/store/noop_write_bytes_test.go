package store

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// THE QUESTION THIS ANSWERS, and everything about avoiding a 23 GB push rests on
// it: when a step opens the corpus read-write, changes NO rows, checkpoints and
// closes, do the file's BYTES stay identical?
//
// Why the bytes and not the mtime: scripts/local/imgtar sets every tar header's
// ModTime to a fixed epoch precisely so a layer digest depends on content alone.
// So an unchanged corpus produces a byte-identical tar, the registry answers
// "existing blob", and the upload costs nothing — even when `publish` re-runs
// because the file's mtime moved.
//
// Measured before this test existed: `enrich` rewrites ~40 000 rows on every run
// whether or not anything changed (ingest-catalog reports all 3 565 specs
// "overlaid" every time), the corpus grows ~5.8 MB, the layer takes a new digest,
// and 23 GB crosses the wire — set off by a 2-byte change in an upstream HTML
// report. Making those writers genuinely idempotent is only worth doing if the
// property below holds.
func TestNoOpWriteLeavesTheFileByteIdentical(t *testing.T) {
	path, cleanup := scratchDB(t)
	defer cleanup()

	seedOneSpec(t, path)
	before := sha(t, path)

	// What an idempotent overlay does: an UPDATE that matches no row, then a
	// checkpoint, then close. This is the shape ingest-catalog takes once its
	// UPDATE compares values instead of matching on spec_id alone.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	res, err := s.DB().Exec(
		`UPDATE specs SET title = ? WHERE spec_id = ? AND title IS DISTINCT FROM ?`,
		"System architecture", "23.501", "System architecture")
	if err != nil {
		_ = s.Close()
		t.Fatalf("no-op update: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		_ = s.Close()
		t.Fatalf("the no-op update touched %d row(s); the premise of this test is wrong", n)
	}
	if _, err := s.DB().Exec(`CHECKPOINT`); err != nil {
		_ = s.Close()
		t.Fatalf("checkpoint: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if after := sha(t, path); before != after {
		t.Fatalf("a no-op write CHANGED the file (%s -> %s).\n"+
			"Making the overlay writers idempotent would not stop the layer digest from "+
			"moving, so it would not stop the push either — the saving has to come from "+
			"somewhere else.", before[:16], after[:16])
	}
}

// THE NEGATIVE CONTROL. If the check above passed because the file never changes
// for any reason, it would prove nothing at all.
func TestARealWriteDoesChangeTheFile(t *testing.T) {
	path, cleanup := scratchDB(t)
	defer cleanup()

	seedOneSpec(t, path)
	before := sha(t, path)

	s, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := s.DB().Exec(`UPDATE specs SET title = 'Something else' WHERE spec_id = '23.501'`); err != nil {
		_ = s.Close()
		t.Fatalf("real update: %v", err)
	}
	if _, err := s.DB().Exec(`CHECKPOINT`); err != nil {
		_ = s.Close()
		t.Fatalf("checkpoint: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if sha(t, path) == before {
		t.Fatal("a REAL write left the file byte-identical; the comparison above is measuring nothing")
	}
}

// seedOneSpec puts a row in the schema Open() migrates into place, then
// checkpoints so the baseline is a settled file rather than one with a pending
// write-ahead log.
func seedOneSpec(t *testing.T, path string) {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.DB().Exec(
		`INSERT INTO specs (spec_id, series, title, doc_type, working_group)
		 VALUES ('23.501','23','System architecture','TS','SA2')`); err != nil {
		_ = s.Close()
		t.Fatalf("insert: %v", err)
	}
	if _, err := s.DB().Exec(`CHECKPOINT`); err != nil {
		_ = s.Close()
		t.Fatalf("checkpoint: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// scratchDB is used instead of t.TempDir() because DuckDB can still hold the
// file for a moment after Close() on Windows, and TempDir's cleanup turns that
// into a test failure that has nothing to do with what is being measured.
func scratchDB(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "noopwrite")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	return filepath.Join(dir, "corpus.duckdb"), func() { _ = os.RemoveAll(dir) }
}

func sha(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatalf("hash %s: %v", path, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}
