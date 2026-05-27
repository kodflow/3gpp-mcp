// Package bootstrap fetches the runtime artifacts the server needs but does not
// ship in the binary: the indexed DuckDB snapshot and (for the semantic path)
// the ONNX models + runtime. It downloads them once into a per-user cache,
// verifies SHA-256, and decompresses transparently — so `mcp-3gpp serve` on a
// fresh machine becomes self-provisioning (CLAUDE.md: mono-binaire, local-first).
//
// The binary NEVER pulls a container or runs a daemon: it downloads plain
// artifacts (DB from a release, models from HuggingFace) and reads them locally.
package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// CacheDir is the per-user cache root for mcp-3gpp artifacts. Override with
// MCP3GPP_CACHE; otherwise os.UserCacheDir()/mcp-3gpp.
func CacheDir() (string, error) {
	if d := os.Getenv("MCP3GPP_CACHE"); d != "" {
		return d, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve cache dir: %w", err)
	}
	return filepath.Join(base, "mcp-3gpp"), nil
}

// DBPath is the cached DuckDB snapshot path.
func DBPath() (string, error) {
	d, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "3gpp.duckdb"), nil
}

// ModelsDir is the cached ONNX models/runtime root.
func ModelsDir() (string, error) {
	d, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "models"), nil
}

// Artifact is one downloadable file.
type Artifact struct {
	URL    string // http(s) source; .zst is decompressed transparently
	SHA256 string // hex of the FINAL (decompressed) bytes; "" = skip verification
	Dest   string // absolute destination path
}

// HTTPClient is the client used for downloads (overridable in tests).
var HTTPClient = http.DefaultClient

// Fetch downloads a.URL to a.Dest atomically, decompressing .zst by extension
// and verifying SHA-256 over the final bytes. It is idempotent: a present file
// whose hash matches (or any present file when SHA256 is empty) is kept.
func Fetch(ctx context.Context, a Artifact) error {
	if fileExists(a.Dest) {
		if a.SHA256 == "" {
			return nil
		}
		if sum, err := sha256File(a.Dest); err == nil && sum == strings.ToLower(a.SHA256) {
			return nil
		}
		// present but stale/corrupt → re-fetch
	}
	if err := os.MkdirAll(filepath.Dir(a.Dest), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "mcp-3gpp-bootstrap")
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("get %s: %w", a.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("get %s: status %s", a.URL, resp.Status)
	}

	var src io.Reader = resp.Body
	if strings.HasSuffix(a.URL, ".zst") {
		zr, err := zstd.NewReader(resp.Body)
		if err != nil {
			return fmt.Errorf("zstd %s: %w", a.URL, err)
		}
		defer zr.Close()
		src = zr
	}

	tmp := a.Dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), src); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("download %s: %w", a.URL, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if a.SHA256 != "" {
		if got := hex.EncodeToString(h.Sum(nil)); got != strings.ToLower(a.SHA256) {
			_ = os.Remove(tmp)
			return fmt.Errorf("sha256 mismatch for %s: got %s want %s", a.Dest, got, a.SHA256)
		}
	}
	return os.Rename(tmp, a.Dest)
}

// FetchAll fetches artifacts sequentially, stopping at the first error.
func FetchAll(ctx context.Context, arts []Artifact) error {
	for _, a := range arts {
		if err := Fetch(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

// SHA256File returns the lowercase hex SHA-256 of the file at path. Used to
// compare a cached DB against the hash published alongside the rolling release.
func SHA256File(path string) (string, error) { return sha256File(path) }

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
