package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ONE FILE MUST NOT HAVE TWO CONFIGURATION KEYS.
//
// The absence register has a WRITER and a READER, and they read different
// variables: scripts/etsi-fetch.sh takes ETSI_ABSENCES, scripts/data-contract.sh
// takes DATA_ETSI_ABSENCES. An operator who moved the register with the writer's
// key alone would have the fetch record its absences in one file while
// validate-etsi looked in another.
//
// WHAT MAKES THAT WORSE THAN A PLAIN MISCONFIGURATION: the gate does not fail on
// a register it cannot find. It reports UNVERIFIED — "this corpus predates the
// register" — which is exactly the sentence a legacy corpus produces. The
// divergence would therefore read as an expected state, on a build where four
// deliverables were in fact recorded and ignored.
//
// So the writer's key wins when it is set, and the two names become one setting.
func TestTheAbsenceRegisterHasOneKey(t *testing.T) {
	root := testRepoRoot(t)

	// A work list must exist, or the contract omits the flags entirely (they are
	// build artefacts, and the same contract runs inside the image without them).
	wl := filepath.Join(t.TempDir(), "worklist.tsv")
	if err := os.WriteFile(wl, []byte("102 221\thttps://x/a.pdf\t11.0.0\tTS\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATA_ETSI_WORKLIST", wl)
	t.Setenv("DATA_CONTRACT", "")

	moved := filepath.Join(t.TempDir(), "somewhere-else.tsv")
	t.Setenv("ETSI_ABSENCES", moved)
	t.Setenv("DATA_ETSI_ABSENCES", "") // the operator set only the writer's key

	flags := dataContractFlags(root, armETSI)
	if !strings.Contains(flags, "--absences "+moved) {
		t.Errorf("the gate reads a different register than the fetch writes.\n"+
			"ETSI_ABSENCES=%s\ncontract: %s", moved, flags)
	}

	// And the reader's key still wins when it is the one set explicitly: an
	// operator naming DATA_ETSI_ABSENCES is pointing the GATE somewhere on purpose.
	other := filepath.Join(t.TempDir(), "gate-only.tsv")
	t.Setenv("DATA_ETSI_ABSENCES", other)
	if flags := dataContractFlags(root, armETSI); !strings.Contains(flags, "--absences "+other) {
		t.Errorf("an explicit DATA_ETSI_ABSENCES was overridden: %s", flags)
	}
}
