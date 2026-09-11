package main

import (
	"os"
	"path/filepath"
	"testing"
)

// --etsi-db off serves the 3GPP half alone even with an etsi.duckdb beside it;
// empty keeps the "beside" default; a path is taken as given.
func TestResolveETSIPath(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "3gpp.duckdb")
	beside := filepath.Join(dir, "etsi.duckdb")

	if p, why := resolveETSIPath("", db); p != "" || why != "" {
		t.Errorf("no etsi.duckdb beside and no flag: attached %q (%s)", p, why)
	}
	if err := os.WriteFile(beside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, _ := resolveETSIPath("", db); p != beside {
		t.Errorf("empty flag with an etsi.duckdb beside: attached %q, want %q", p, beside)
	}
	if p, why := resolveETSIPath(etsiOff, db); p != "" || why == "" {
		t.Errorf("--etsi-db off with an etsi.duckdb beside: attached %q (%q) — the default could not be declined", p, why)
	}
	if p, _ := resolveETSIPath("elsewhere.duckdb", db); p != "elsewhere.duckdb" {
		t.Errorf("an explicit path was not taken as given: %q", p)
	}
}
