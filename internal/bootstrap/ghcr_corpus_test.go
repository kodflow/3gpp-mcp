package bootstrap

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestFetchCorpusSkipsTransferWhenCacheIsCurrent pins the promise docs/install.md
// makes: re-running a fetch against an unchanged package costs one manifest
// request and nothing else.
//
// It is a regression test for a real split. `serve` asked "is the cache already
// this corpus" before transferring; the `bootstrap` command and the pipeline's
// seed step called FetchCorpus directly and did not, so either one pulled the
// whole 7.9 GB layer to reproduce a file that was already on disk byte for byte.
// The check now lives in FetchCorpus, where every caller gets it.
func TestFetchCorpusSkipsTransferWhenCacheIsCurrent(t *testing.T) {
	const member = "3gpp.duckdb"
	payload := []byte("not a real DuckDB file, but it is the bytes that get compared")

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: member, Mode: 0o644, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	layer := buf.Bytes()
	sum := sha256.Sum256(layer)
	digest := "sha256:" + hex.EncodeToString(sum[:])

	var blobHits, manifestHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			fmt.Fprint(w, `{"token":"test-token"}`)
		case strings.Contains(r.URL.Path, "/manifests/"):
			atomic.AddInt32(&manifestHits, 1)
			fmt.Fprintf(w, `{"layers":[{"digest":%q,"size":%d}]}`, digest, len(layer))
		case strings.Contains(r.URL.Path, "/blobs/"):
			atomic.AddInt32(&blobHits, 1)
			_, _ = w.Write(layer)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	oldBase, oldService := registryBase, registryService
	registryBase, registryService = srv.URL, "test"
	defer func() { registryBase, registryService = oldBase, oldService }()

	src := CorpusSource{Owner: "o", Image: "i", Ref: "latest", Member: member}
	dest := filepath.Join(t.TempDir(), member)
	quiet := func(string, ...any) {}

	if err := FetchCorpus(context.Background(), src, "pat", dest, quiet); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("the member was not extracted: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("extracted %q, want %q", got, payload)
	}
	if n := atomic.LoadInt32(&blobHits); n != 1 {
		t.Fatalf("first fetch pulled the blob %d times, want 1", n)
	}

	// Same package, same cache: the transfer is what must not happen again.
	if err := FetchCorpus(context.Background(), src, "pat", dest, quiet); err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if n := atomic.LoadInt32(&blobHits); n != 1 {
		t.Fatalf("cache was already current, yet the blob was pulled %d times total", n)
	}
	if n := atomic.LoadInt32(&manifestHits); n != 2 {
		t.Fatalf("manifest read %d times, want 2 — one per call", n)
	}
}

// TestFetchCorpusTransfersWhenThePublishedLayerChanges is the other half: the
// short-circuit must key on the published identity, not merely on the file
// being present, or a new corpus would never reach an existing cache.
func TestFetchCorpusTransfersWhenThePublishedLayerChanges(t *testing.T) {
	const member = "3gpp.duckdb"

	tarOf := func(body string) []byte {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		_ = tw.WriteHeader(&tar.Header{Name: member, Mode: 0o644, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
		_ = tw.Close()
		return buf.Bytes()
	}
	first, second := tarOf("corpus v1"), tarOf("corpus v2 — reindexed")
	digestOf := func(b []byte) string { s := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(s[:]) }

	current := first
	var blobHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			fmt.Fprint(w, `{"token":"test-token"}`)
		case strings.Contains(r.URL.Path, "/manifests/"):
			fmt.Fprintf(w, `{"layers":[{"digest":%q,"size":%d}]}`, digestOf(current), len(current))
		case strings.Contains(r.URL.Path, "/blobs/"):
			atomic.AddInt32(&blobHits, 1)
			_, _ = w.Write(current)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	oldBase, oldService := registryBase, registryService
	registryBase, registryService = srv.URL, "test"
	defer func() { registryBase, registryService = oldBase, oldService }()

	src := CorpusSource{Owner: "o", Image: "i", Ref: "latest", Member: member}
	dest := filepath.Join(t.TempDir(), member)
	quiet := func(string, ...any) {}

	if err := FetchCorpus(context.Background(), src, "pat", dest, quiet); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	current = second // a new corpus is published
	if err := FetchCorpus(context.Background(), src, "pat", dest, quiet); err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if n := atomic.LoadInt32(&blobHits); n != 2 {
		t.Fatalf("the published layer changed, yet the blob was pulled %d times, want 2", n)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "corpus v2") {
		t.Fatalf("the cache still holds %q — the update did not land", got)
	}
}
