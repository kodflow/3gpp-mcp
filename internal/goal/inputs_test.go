package goal

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNoStepFingerprintsADirectory.
//
// inputsHash identifies an input by size and mtime, and for a DIRECTORY it
// records the constant string "dir" (fingerprint.go). A declared directory is
// therefore not a weak determinant, it is not a determinant at all: its recorded
// value is the same forever, whatever happens inside it.
//
// It reads exactly like watching a tree, which is how `enrich` came to declare
// data/sources/5g-apis and data/sources/asn — with a comment saying, in as many
// words, that acquiring the LI registry had to make the overlay dirty. It never
// did. Every 5GC API and every ASN.1 module could be added, changed or deleted
// and the overlay stayed "unchanged".
//
// The tree below is created by the test, so this is deterministic rather than a
// property of whatever happens to be on the machine.
func TestNoStepFingerprintsADirectory(t *testing.T) {
	c, _ := newTestCtx(t)

	// The directories a step might plausibly name, each with a file inside so
	// that enumerating one yields something and naming one yields a directory.
	dirs := []string{
		filepath.Join(c.Data, "sources", "5g-apis"),
		filepath.Join(c.Data, "sources", "asn"),
		filepath.Join(c.Data, "sources", "convert"),
		filepath.Join(c.Data, "sources", "convert-etsi"),
		filepath.Join(c.Data, "sources", "origin"),
		filepath.Join(c.Data, "sources", "etsi-origin"),
		filepath.Join(c.Data, "models", "bge-m3"),
		filepath.Join(c.Data, "models", "bge-m3-sparse"),
		filepath.Join(c.Data, "models", "bge-reranker-v2-m3"),
		filepath.Join(c.Data, "models", "onnxruntime"),
		filepath.Join(c.Data, "shards"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "member"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for _, s := range Pipeline() {
		if s.Inputs == nil {
			continue
		}
		ins, err := s.Inputs(c)
		if err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
		for _, in := range ins {
			st, err := os.Stat(in)
			if err != nil || !st.IsDir() {
				continue
			}
			t.Errorf("step %s declares the directory %s as an input: inputsHash records it as "+
				"the constant \"dir\", so nothing that happens inside it can ever invalidate the step",
				s.Name, in)
		}
	}
}

// TestDiscoverDoesNotTouchAReportThatDidNotChange.
//
// The status report is discover's own HTTP cache AND an input of `enrich`, which
// writes the corpus. Publishing it with os.Rename moved its mtime on every run,
// so every discover — and discover runs on a 6 h TTL, a clock rather than a
// change — made enrich dirty and replayed paragraphs, sparse, compact, index,
// validate and smoke behind it: ~21 minutes of corpus work, three builds in a
// row, for a corpus nothing had been added to.
//
// The negative control matters as much: a report that DID change must still be
// published, or discover would stop seeing what 3GPP publishes.
func TestDiscoverDoesNotTouchAReportThatDidNotChange(t *testing.T) {
	c, _ := newTestCtx(t)
	report := c.statePath("status-report.htm")
	if err := os.MkdirAll(filepath.Dir(report), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("<html>the 3GPP status report</html>")
	if err := WriteAtomic(report, body); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(report)
	if err != nil {
		t.Fatal(err)
	}

	// The same bytes: nothing may move.
	if err := WriteAtomic(report, body); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(report)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("re-publishing an identical status report moved its mtime (%s -> %s): "+
			"enrich would be dirty on a clock, and the whole write side replays behind it",
			before.ModTime(), after.ModTime())
	}

	// Different bytes: it must land, or discover stops seeing new releases.
	if err := WriteAtomic(report, []byte("<html>Rel-22 appeared</html>")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "<html>Rel-22 appeared</html>" {
		t.Errorf("a CHANGED status report was not published: %q", got)
	}
}
