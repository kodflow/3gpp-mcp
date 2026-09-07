package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE GATE MUST BE STRONG ON THE PATH THAT ACTUALLY PUBLISHES.
//
// scripts/data-contract.sh has always been able to demand the sparse layer and
// the ETSI half — and .local/resume/*.sh passed those flags by hand. But
// `make build`, the command that ends in `publish`, sourced only the toolchain
// prelude, so DATA_CONTRACT fell back to "dense" and `validate` ran with:
//
//	--require-fts --require-hnsw --require-embed-complete
//
// Build 23 therefore published a corpus whose 127 476 905 ETSI sparse postings
// and whose entire second half had never been checked by any gate. A corpus that
// lost its sparse layer would have been published without an error and then
// REFUSED TO START on the user's machine, because the image's own entrypoint
// asks for dense+sparse+etsi.
//
// This test reads the flags the way `goal` does, so it fails if the default is
// ever weakened again — in the script or in the Go fallback.
func TestTheDefaultContractDemandsSparseAndETSI(t *testing.T) {
	root := testRepoRoot(t)
	t.Setenv("DATA_CONTRACT", "") // the operator sets nothing: this is `make build`

	flags := dataContractFlags(root)
	for _, want := range []string{
		"--require-fts",
		"--require-hnsw",
		"--require-embed-complete",
		"--require-sparse",
		"--require-etsi",
	} {
		if !strings.Contains(flags, want) {
			t.Errorf("the default contract does not carry %s: %q", want, flags)
		}
	}

	// --require-etsi takes a PATH, and the script's default is the image's
	// (/data/mcp-3gpp/etsi.duckdb), which does not exist in a local build.
	wantPath := filepath.Join(root, "data", "etsi.duckdb")
	if !strings.Contains(flags, wantPath) {
		t.Errorf("--require-etsi does not point at this build's corpus (%s): %q", wantPath, flags)
	}
}

// Loosening must stay possible — a ratchet nobody can lower is a ratchet people
// route around. It just has to be something a person typed.
func TestTheContractCanStillBeLoosenedDeliberately(t *testing.T) {
	root := testRepoRoot(t)
	t.Setenv("DATA_CONTRACT", "dense")

	flags := dataContractFlags(root)
	if strings.Contains(flags, "--require-sparse") {
		t.Errorf("DATA_CONTRACT=dense still demanded the sparse layer: %q", flags)
	}
	if !strings.Contains(flags, "--require-fts") {
		t.Errorf("DATA_CONTRACT=dense dropped the dense contract too: %q", flags)
	}
}

func testRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd() // <root>/cmd/goal
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(wd))
	if _, err := os.Stat(filepath.Join(root, "scripts", "data-contract.sh")); err != nil {
		t.Skipf("scripts/data-contract.sh not reachable from %s: %v", wd, err)
	}
	return root
}

// AN OPERATOR-SUPPLIED DATA_ETSI_DB MUST WIN.
//
// The repo path is a DEFAULT, not an override. It used to be appended to
// os.Environ() unconditionally, and later entries take precedence in exec's
// environment — so a corpus kept somewhere else was silently checked at the repo
// path instead. Silently checking the wrong file is worse than checking none:
// the gate still reports [ok].
func TestAnOperatorSuppliedETSIPathIsNotOverridden(t *testing.T) {
	root := testRepoRoot(t)
	custom := filepath.Join(t.TempDir(), "elsewhere.duckdb")
	t.Setenv("DATA_ETSI_DB", custom)

	flags := dataContractFlags(root)
	if !strings.Contains(flags, custom) {
		t.Errorf("the operator's DATA_ETSI_DB was ignored: %q", flags)
	}
	if strings.Contains(flags, filepath.Join(root, "data", "etsi.duckdb")) {
		t.Errorf("the repo default overrode the operator's path: %q", flags)
	}
}
