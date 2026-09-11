package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"usr/local/bin/docker-entrypoint.sh": "#!/bin/sh\nexec \"$@\"\n",
		"data/models.yaml":                   "models: {}\n",
	} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func key(t *testing.T, root string, paths ...string) string {
	t.Helper()
	entries, err := collect(root, paths)
	if err != nil {
		t.Fatal(err)
	}
	k, err := layerKey(entries, 10001, 10001, true)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// THE GZIP LAYER IS THE TAR, GZIPPED AT BESTSPEED WITH A DEFAULT HEADER — the
// stream go-containerregistry produces for an uncompressed layer, which is what
// keeps the blob digest the registry already holds. Checked here against the
// plain tar this same packer writes; the crane equivalence itself was measured
// (see writeLayer).
func TestTheGzipLayerIsThePlainLayerGzippedAtBestSpeed(t *testing.T) {
	root := tree(t)
	entries, err := collect(root, []string{"usr", "data"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	plain, gz := filepath.Join(dir, "l.tar"), filepath.Join(dir, "l.tar.gz")
	if _, err := writeLayer(plain, entries, 10001, 10001, false); err != nil {
		t.Fatal(err)
	}
	if _, err := writeLayer(gz, entries, 10001, 10001, true); err != nil {
		t.Fatal(err)
	}
	p, _ := os.ReadFile(plain)
	var want bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&want, gzip.BestSpeed)
	_, _ = zw.Write(p)
	_ = zw.Close()
	got, _ := os.ReadFile(gz)
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatal("the gzip layer is not the plain layer through compress/gzip at BestSpeed: its digest " +
			"would differ from crane's and every layer would be re-uploaded")
	}
	zr, err := gzip.NewReader(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	back, _ := io.ReadAll(zr)
	if !bytes.Equal(back, p) {
		t.Fatal("the gzip layer does not decompress to the plain layer")
	}
}

// A SMALL FILE IS KEYED BY ITS CONTENT: the script rewrites models.yaml and the
// entrypoint on every build, so an mtime key would never hit. Touching one keeps
// the key; changing a byte moves it.
func TestASmallFileIsKeyedByContentNotMtime(t *testing.T) {
	root := tree(t)
	before := key(t, root, "usr", "data")
	later := time.Now().Add(time.Hour)
	yaml := filepath.Join(root, "data", "models.yaml")
	if err := os.Chtimes(yaml, later, later); err != nil {
		t.Fatal(err)
	}
	if key(t, root, "usr", "data") != before {
		t.Fatal("touching a small file moved the key: a regenerated models.yaml would repack 6.4 GB of model")
	}
	if err := os.WriteFile(yaml, []byte("models: {x: 1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if key(t, root, "usr", "data") == before {
		t.Fatal("changing a small file's content kept the key: the stale layer would be reused")
	}
}

// A LARGE FILE IS KEYED BY SIZE AND MTIME: the corpus is 23 GB and reaches the
// rootfs by hard link, so its mtime is the corpus's own. A rewrite moves it.
func TestALargeFileIsKeyedBySizeAndMtime(t *testing.T) {
	root := t.TempDir()
	big := filepath.Join(root, "corpus.duckdb")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(contentKeyMax + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	before := key(t, root, "corpus.duckdb")
	if key(t, root, "corpus.duckdb") != before {
		t.Fatal("the key of an untouched large file is not stable")
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(big, later, later); err != nil {
		t.Fatal(err)
	}
	if key(t, root, "corpus.duckdb") == before {
		t.Fatal("a rewritten (re-stamped) corpus kept its key: last build's layer would be shipped for it")
	}
}

// THE CACHE HANDS BACK A COMPLETE LAYER FOR EXACTLY ITS KEY, and a key written
// last is what makes that true: no key, or another key, is a miss.
func TestTheCacheHitsOnlyOnItsOwnKey(t *testing.T) {
	root := tree(t)
	entries, _ := collect(root, []string{"usr", "data"})
	k := key(t, root, "usr", "data")
	dir := t.TempDir()
	layer := filepath.Join(dir, "10-x.tar.gz")
	if _, err := writeLayer(layer, entries, 10001, 10001, true); err != nil {
		t.Fatal(err)
	}
	blob := filepath.Join(dir, "cache", "10-x.tar.gz")
	if hit(blob, k) {
		t.Fatal("an empty cache hit")
	}
	if err := store(blob, k, layer, identity{}); err != nil {
		t.Fatal(err)
	}
	if !hit(blob, k) {
		t.Fatal("a stored layer did not hit under its own key")
	}
	if hit(blob, k+"0") {
		t.Fatal("a stored layer hit under another key")
	}
	_ = os.Remove(blob + ".key")
	if hit(blob, k) {
		t.Fatal("a blob with no key hit: an interrupted store would be reused as complete")
	}
}

// A LARGE FILE REWRITTEN UNDER ITS OLD SIZE AND MTIME MISSES: the content sample
// covers offset 0, where a DuckDB file's header changes on every checkpoint.
func TestALargeFileRewrittenUnderItsOldMtimeMisses(t *testing.T) {
	root := t.TempDir()
	big := filepath.Join(root, "corpus.duckdb")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(contentKeyMax + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	st, _ := os.Stat(big)
	before := key(t, root, "corpus.duckdb")

	f, err = os.OpenFile(big, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("HEADER CHANGED BY A CHECKPOINT"), 0); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := os.Chtimes(big, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	if st2, _ := os.Stat(big); st2.Size() != st.Size() || !st2.ModTime().Equal(st.ModTime()) {
		t.Fatal("the test did not restore size and mtime, so it proves nothing")
	}
	if key(t, root, "corpus.duckdb") == before {
		t.Fatal("a corpus rewritten under its old size and mtime kept its key: the stale layer would ship")
	}
}
