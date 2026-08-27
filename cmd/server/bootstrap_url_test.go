package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodflow/3gpp-mcp/internal/bootstrap"
)

// TestBootstrapDefaultsAreNotAPublicReleaseAsset pins the compliance rule that
// the previous default broke.
//
// `defaultDBURL` used to be
// https://github.com/kodflow/3gpp-mcp/releases/download/latest/3gpp.duckdb.zst —
// a PUBLIC asset on a PUBLIC repository, carrying verbatim 3GPP clause text.
// DATA_NOTICE.md forbids exactly that, and because it was the DEFAULT, the
// out-of-the-box path was the non-compliant one. The corpus now travels as a
// private GHCR package.
//
// This test is deliberately about the SHAPE of the default, not about GHCR: any
// future change that re-points the default at a github.com release asset — for a
// mirror, for convenience, for a test — must fail here and be argued explicitly.
func TestBootstrapDefaultsAreNotAPublicReleaseAsset(t *testing.T) {
	t.Setenv(envGHCROwner, "")
	t.Setenv(envCorpusTag, "")

	for name, src := range map[string]bootstrap.CorpusSource{
		"corpusSource": corpusSource(),
		"etsiSource":   etsiSource(),
	} {
		got := src.String()
		if strings.Contains(got, "github.com") || strings.Contains(got, "/releases/") {
			t.Errorf("%s resolves to a GitHub release asset (%s).\n"+
				"DATA_NOTICE.md: no Release asset of this public repository may carry a\n"+
				"full-text DuckDB database. The corpus belongs in the private GHCR package.", name, got)
		}
		if !strings.HasPrefix(got, "ghcr.io/") {
			t.Errorf("%s should resolve to a ghcr.io package, got %q", name, got)
		}
	}
}

// TestCorpusSourceIsRepointableWithoutARebuild covers the deployment seam: a fork
// or a staging tag must not require rebuilding the binary.
func TestCorpusSourceIsRepointableWithoutARebuild(t *testing.T) {
	t.Setenv(envGHCROwner, "someone-else")
	t.Setenv(envCorpusTag, "2026-08-26")

	if got, want := corpusSource().String(), "ghcr.io/someone-else/3gpp-corpus:2026-08-26"; got != want {
		t.Errorf("corpusSource() = %q, want %q", got, want)
	}
	if got, want := etsiSource().String(), "ghcr.io/someone-else/etsi-corpus:2026-08-26"; got != want {
		t.Errorf("etsiSource() = %q, want %q", got, want)
	}
}

// TestEnsureDBWithoutACredentialKeepsACachedCorpus covers the degrade path that
// matters most in practice: a machine that already holds the corpus must keep
// serving when no token is around, rather than refusing to start.
func TestEnsureDBWithoutACredentialKeepsACachedCorpus(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("MCP3GPP_CACHE", cache)
	t.Setenv("GHCR_PAT", "")
	t.Setenv("GITHUB_TOKEN", "")
	// GHCRCredential's last resort reads ./.local/ghcr.pat; run from a directory
	// that has none so the test is about the credential being ABSENT.
	t.Chdir(t.TempDir())

	dbPath := filepath.Join(cache, "3gpp.duckdb")
	if err := os.WriteFile(dbPath, []byte("not really duckdb, but present"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ensureDB(context.Background(), true)
	if err != nil {
		t.Fatalf("ensureDB with a cached corpus and no credential: %v", err)
	}
	if got != dbPath {
		t.Errorf("ensureDB = %q, want the cached corpus %q", got, dbPath)
	}
}

// TestEnsureDBWithoutACredentialOrACacheExplainsItself is the other half: with
// nothing cached there is nothing to serve, and the error has to tell the user
// how to get a token rather than surfacing a bare 401.
func TestEnsureDBWithoutACredentialOrACacheExplainsItself(t *testing.T) {
	t.Setenv("MCP3GPP_CACHE", t.TempDir())
	t.Setenv("GHCR_PAT", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Chdir(t.TempDir())

	_, err := ensureDB(context.Background(), true)
	if err == nil {
		t.Fatal("ensureDB should fail with no cache and no credential")
	}
	for _, want := range []string{"read:packages", "GHCR_PAT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q, so the user cannot act on it:\n%v", want, err)
		}
	}
}
