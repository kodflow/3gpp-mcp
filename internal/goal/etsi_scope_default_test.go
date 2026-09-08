package goal

import (
	"slices"
	"strings"
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
	wantEnv := []string{"ETSI_ALL=1", "ETSI_ALL_VERSIONS=1", "ETSI_SPECS="}
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
	// AND IT MUST CLEAR, not merely stay silent. Ctx.Run passes cmd.Env as
	// append(os.Environ(), ...), so a variable this function does not mention is
	// inherited: returning nil here let an ambient ETSI_ALL reach
	// scripts/etsi-fetch.sh, which rebuilds its own work list from it. Discovery
	// would resolve fourteen deliverables from FLAGS while the fetch downloaded the
	// whole archive — one arm, two corpora, and the only symptom a fetch that runs
	// all night when fourteen specs were asked for.
	wantCleared := []string{"ETSI_ALL=", "ETSI_ALL_VERSIONS=", "ETSI_SPECS="}
	if got := etsiScopeEnv(ScopeLISuite); !slices.Equal(got, wantCleared) {
		t.Errorf("li-suite passes env %v, want %v", got, wantCleared)
	}
}

// NO SCOPE MAY LEAVE A VARIABLE TO THE AMBIENT ENVIRONMENT. Every scope must say
// something about every variable the fetch script reads, or the operator's shell
// decides what the pipeline downloads.
func TestEveryScopeSpeaksForEveryFetchVariable(t *testing.T) {
	read := []string{"ETSI_ALL=", "ETSI_ALL_VERSIONS=", "ETSI_SPECS="}
	for _, scope := range []string{"", ScopeAll, ScopeAllVersions, ScopeLISuite, "103 221-1"} {
		env := etsiScopeEnv(scope)
		for _, want := range read {
			found := false
			for _, e := range env {
				if strings.HasPrefix(e, want) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("scope %q says nothing about %s, so the shell decides it: %v",
					scope, strings.TrimSuffix(want, "="), env)
			}
		}
	}
}

// A DETERMINANT MUST NAME WHAT THE STEP WILL DO, NOT WHAT THE OPERATOR TYPED.
//
// discover-etsi recorded c.Cfg("etsi_scope"), and the empty string is the value
// almost every run carries. So the day the empty string stopped meaning "the
// fourteen built-in LI deliverables" and started meaning "the whole archive, every
// version", the recorded determinant did not move: the step SKIPPED, the fix
// shipped inert, and every gate stayed green over an ETSI half discovered exactly
// as narrowly as before.
//
// The test is the two scopes that differ in MEANING while sharing a knob value's
// shape: an unset scope and the named narrow one must record different things.
func TestTheRecordedScopeIsWhatTheStepWillDo(t *testing.T) {
	byName := map[string]*Step{}
	for _, s := range Pipeline() {
		byName[s.Name] = s
	}
	for _, name := range []string{"discover-etsi", "fetch-etsi"} {
		s := byName[name]
		if s == nil || s.Extra == nil {
			t.Errorf("%s records no scope determinant, so a change of scope cannot replay it", name)
			continue
		}
		c, _ := newTestCtx(t)

		c.Config["etsi_scope"] = ""
		wide, err := s.Extra(c)
		if err != nil {
			t.Fatal(err)
		}
		c.Config["etsi_scope"] = ScopeLISuite
		narrow, err := s.Extra(c)
		if err != nil {
			t.Fatal(err)
		}
		if wide["etsi_scope"] == narrow["etsi_scope"] {
			t.Errorf("%s records %q for BOTH the whole archive and the LI suite: a change "+
				"between them cannot move the fingerprint, so the step would skip",
				name, wide["etsi_scope"])
		}
		if wide["etsi_scope"] == "" {
			t.Errorf("%s records the empty string for the whole archive — the knob's value, "+
				"not the work", name)
		}
	}
}
