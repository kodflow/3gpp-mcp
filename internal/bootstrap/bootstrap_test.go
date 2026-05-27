package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// serves a payload at /plain and a zstd-compressed copy at /comp.zst, counting hits.
func newServer(t *testing.T, payload []byte) (*httptest.Server, *int64) {
	t.Helper()
	var comp bytes.Buffer
	zw, _ := zstd.NewWriter(&comp)
	_, _ = zw.Write(payload)
	_ = zw.Close()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		switch r.URL.Path {
		case "/plain":
			_, _ = w.Write(payload)
		case "/comp.zst":
			_, _ = w.Write(comp.Bytes())
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestFetchVerifiesAndIsIdempotent(t *testing.T) {
	payload := []byte("indexed-duckdb-bytes-stand-in")
	srv, hits := newServer(t, payload)
	dest := filepath.Join(t.TempDir(), "3gpp.duckdb")
	art := Artifact{URL: srv.URL + "/plain", SHA256: sha(payload), Dest: dest}

	if err := Fetch(context.Background(), art); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, payload) {
		t.Fatalf("content mismatch: %q", got)
	}
	if *hits != 1 {
		t.Fatalf("want 1 download, got %d", *hits)
	}
	// Idempotent: a present, hash-matching file is not re-downloaded.
	if err := Fetch(context.Background(), art); err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if *hits != 1 {
		t.Errorf("idempotency broken: re-downloaded (hits=%d)", *hits)
	}
}

func TestFetchRejectsBadSHA(t *testing.T) {
	payload := []byte("real-bytes")
	srv, _ := newServer(t, payload)
	dest := filepath.Join(t.TempDir(), "f.bin")
	err := Fetch(context.Background(), Artifact{URL: srv.URL + "/plain", SHA256: sha([]byte("tampered")), Dest: dest})
	if err == nil {
		t.Fatal("expected sha256 mismatch error")
	}
	if fileExists(dest) {
		t.Error("corrupt download must not be left at dest")
	}
}

func TestFetchDecompressesZstd(t *testing.T) {
	payload := bytes.Repeat([]byte("clause "), 1000) // compresses well
	srv, _ := newServer(t, payload)
	dest := filepath.Join(t.TempDir(), "out.bin")
	// SHA256 is over the DECOMPRESSED bytes.
	err := Fetch(context.Background(), Artifact{URL: srv.URL + "/comp.zst", SHA256: sha(payload), Dest: dest})
	if err != nil {
		t.Fatalf("fetch zst: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, payload) {
		t.Fatalf("decompressed content mismatch (len got=%d want=%d)", len(got), len(payload))
	}
}

func TestCacheDirOverride(t *testing.T) {
	t.Setenv("MCP3GPP_CACHE", "/tmp/custom-cache")
	d, err := CacheDir()
	if err != nil || d != "/tmp/custom-cache" {
		t.Fatalf("CacheDir override: %q err=%v", d, err)
	}
	db, _ := DBPath()
	if db != "/tmp/custom-cache/3gpp.duckdb" {
		t.Errorf("DBPath = %q", db)
	}
}
