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

	flags := dataContractFlags(root, arm3GPP)
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

	flags := dataContractFlags(root, arm3GPP)
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

	flags := dataContractFlags(root, arm3GPP)
	if !strings.Contains(flags, custom) {
		t.Errorf("the operator's DATA_ETSI_DB was ignored: %q", flags)
	}
	if strings.Contains(flags, filepath.Join(root, "data", "etsi.duckdb")) {
		t.Errorf("the repo default overrode the operator's path: %q", flags)
	}
}

// THE FALLBACK MUST CHECK THE SAME CORPUS THE SCRIPT WOULD HAVE.
//
// The fallback path used to hardcode the repo's data/etsi.duckdb, so when the
// script failed AND the operator had pointed DATA_ETSI_DB elsewhere, the gate
// silently switched to a different corpus — only in the branch that already means
// something went wrong, which is the hardest case to notice.
func TestTheFallbackHonoursTheOperatorsETSIPath(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "elsewhere.duckdb")
	t.Setenv("DATA_ETSI_DB", custom)

	// A root with no scripts/data-contract.sh forces the fallback branch.
	flags := dataContractFlags(t.TempDir(), arm3GPP)
	if !strings.Contains(flags, "--require-etsi "+custom) {
		t.Errorf("the fallback ignored the operator's DATA_ETSI_DB: %q", flags)
	}
}

// THE ETSI HALF IS HELD TO THE SAME CONTRACT, and this is the check that had no
// gate at all.
//
// `validate` used to be one step: it ran the whole contract on data/3gpp.duckdb
// and judged the ETSI corpus by the single composite --require-etsi. So the 3GPP
// half answered for --require-fts, --require-hnsw, --require-embed-complete and
// --require-sparse, and the ETSI half -- the same writers, the same freeze, a
// corpus of comparable size -- answered for none of them. An ETSI FTS index that
// failed to build, or a sparse layer that came out empty, could not fail any gate
// because no gate looked.
func TestTheETSIArmCarriesTheSameChecks(t *testing.T) {
	root := testRepoRoot(t)
	t.Setenv("DATA_CONTRACT", "")

	flags := dataContractFlags(root, armETSI)
	for _, want := range []string{
		"--require-fts",
		"--require-hnsw",
		"--require-embed-complete",
		"--require-sparse",
	} {
		if !strings.Contains(flags, want) {
			t.Errorf("the ETSI arm of the contract does not carry %s: %q", want, flags)
		}
	}
}

// --require-etsi IS THE ONE FLAG THE ETSI ARM MUST NOT CARRY.
//
// It is not a check about a corpus; it is a check about the PAIR -- it opens the
// peer and asserts its embedding identity equals this one's. On the ETSI arm it
// would point the corpus at itself, which passes by construction and proves
// nothing. It stays on the 3GPP arm, once, and that is the whole of the
// asymmetry between the two arms.
func TestTheETSIArmDoesNotCheckItselfAgainstItself(t *testing.T) {
	root := testRepoRoot(t)
	t.Setenv("DATA_CONTRACT", "")

	if flags := dataContractFlags(root, armETSI); strings.Contains(flags, "--require-etsi") {
		t.Errorf("the ETSI arm carries --require-etsi, so it would compare the corpus with itself: %q", flags)
	}
}

// A RELEASE FLOOR MUST NEVER REACH THE ETSI ARM.
//
// --require-embed-complete counts clauses at or above --embed-floor, and
// clauses_needing_embedding skips any clause whose release has no ordinal once a
// floor is set. An ETSI release is the constant "ETSI", which has no ordinal, so
// a floor here selects ZERO clauses: the strongest check in the contract would
// report [ok] over an entirely unvectorised corpus. corpusETSI().Floor already
// documents the same trap one gate earlier, on the embed side.
func TestTheETSIArmNeverCarriesAReleaseFloor(t *testing.T) {
	root := testRepoRoot(t)
	t.Setenv("DATA_CONTRACT", "")
	t.Setenv("DATA_EMBED_FLOOR", "Rel-99")

	if flags := dataContractFlags(root, armETSI); strings.Contains(flags, "--embed-floor") {
		t.Errorf("the ETSI arm took a release floor, which selects zero ETSI clauses: %q", flags)
	}
	// The floor is still the 3GPP arm's, and dropping it there was the defect that
	// failed a complete corpus over 413 pre-Rel-99 GSM-era clauses.
	if flags := dataContractFlags(root, arm3GPP); !strings.Contains(flags, "--embed-floor Rel-99") {
		t.Errorf("the 3GPP arm lost its release floor: %q", flags)
	}
}

// THE FALLBACK IS PER ARM TOO. It runs only when the script could not be asked,
// which is the branch where a wrong contract is hardest to notice -- so it must
// say what the script would have said, on either arm.
func TestTheFallbackKeepsTheArmsApart(t *testing.T) {
	if flags := dataContractFlags(t.TempDir(), armETSI); strings.Contains(flags, "--require-etsi") {
		t.Errorf("the ETSI fallback compares the corpus with itself: %q", flags)
	}
	if flags := dataContractFlags(t.TempDir(), armETSI); !strings.Contains(flags, "--require-sparse") {
		t.Errorf("the ETSI fallback dropped the sparse layer: %q", flags)
	}
}
