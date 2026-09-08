package goal

import (
	"slices"
	"testing"
)

// THE COMMAND THAT PUBLISHES MUST DISCOVER THE WHOLE ARCHIVE.
//
// `make build` runs `goal run` with no -etsi-scope, and nothing sets
// GOAL_ETSI_SCOPE, so whatever the EMPTY value means is what the published corpus
// gets. It used to mean the fourteen built-in Lawful-Interception deliverables,
// while `discover` diffed 20 163 3GPP versions on the other arm.
//
// Nothing could see it. ingest-etsi reads the converted tree — 11 822 files left
// by a campaign run by hand — so the corpus did not shrink and every gate stayed
// green. What was lost was the FUTURE: no ETSI deliverable published after that
// campaign could ever be discovered.
func TestTheDefaultETSIScopeIsTheWholeArchiveEveryVersion(t *testing.T) {
	want := []string{"--all", "--all-versions"}
	if got := etsiScopeArgs(""); !slices.Equal(got, want) {
		t.Errorf("an unset scope resolves to %v, want %v — this is what `make build` uses", got, want)
	}
	if got := etsiScopeArgs("   "); !slices.Equal(got, want) {
		t.Errorf("a whitespace scope resolves to %v, want %v", got, want)
	}
	wantEnv := []string{"ETSI_ALL=1", "ETSI_ALL_VERSIONS=1"}
	if got := etsiScopeEnv(""); !slices.Equal(got, wantEnv) {
		t.Errorf("the script environment for an unset scope is %v, want %v", got, wantEnv)
	}
}

// The two helpers drive the SAME binary by two routes, so a value they disagree
// about indexes one archive through the pipeline and another through the script.
func TestBothScopeRoutesAgree(t *testing.T) {
	for _, scope := range []string{"", ScopeAll, ScopeAllVersions, ScopeLISuite, "103 221-1"} {
		args, env := etsiScopeArgs(scope), etsiScopeEnv(scope)
		all := slices.Contains(args, "--all")
		allEnv := slices.Contains(env, "ETSI_ALL=1")
		if all != allEnv {
			t.Errorf("scope %q: --all=%v but ETSI_ALL=%v", scope, all, allEnv)
		}
		ver := slices.Contains(args, "--all-versions")
		verEnv := slices.Contains(env, "ETSI_ALL_VERSIONS=1")
		if ver != verEnv {
			t.Errorf("scope %q: --all-versions=%v but ETSI_ALL_VERSIONS=%v", scope, ver, verEnv)
		}
	}
}

// Narrowing stays possible, and now has to be typed.
func TestTheLISuiteIsStillReachableByName(t *testing.T) {
	if got := etsiScopeArgs(ScopeLISuite); got != nil {
		t.Errorf("li-suite passes %v, want no scope flags (the binary's built-in list)", got)
	}
	if got := etsiScopeEnv(ScopeLISuite); got != nil {
		t.Errorf("li-suite passes env %v, want none", got)
	}
}
