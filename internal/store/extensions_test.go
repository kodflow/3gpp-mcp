package store

import (
	"context"
	"strings"
	"testing"
)

// TestPrefetchExtensions proves the image-bake step works end to end: INSTALL +
// LOAD of fts and vss through this build's DuckDB, then both report an install
// path. First run needs the network (extensions.duckdb.org) — skip, don't fail,
// when it is unreachable so offline dev runs stay green; CI and the image build
// (which hard-fails) are the enforcing environments.
func TestPrefetchExtensions(t *testing.T) {
	ctx := context.Background()
	if err := PrefetchExtensions(ctx); err != nil {
		if strings.Contains(err.Error(), "install") {
			t.Skipf("extension repository unreachable (offline?): %v", err)
		}
		t.Fatalf("PrefetchExtensions: %v", err)
	}
	paths, err := InstalledExtensionPaths(ctx)
	if err != nil {
		t.Fatalf("InstalledExtensionPaths: %v", err)
	}
	for _, ext := range serveExtensions {
		if paths[ext] == "" {
			t.Errorf("extension %s has no install path after prefetch", ext)
		}
	}
}
