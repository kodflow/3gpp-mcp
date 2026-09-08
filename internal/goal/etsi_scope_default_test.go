package goal

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
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
	// MEMBERSHIP, NOT EQUALITY: the list also carries the variables the pipeline
	// clears because it models no knob for them, and asserting the whole list here
	// would make adding one of those a test failure instead of a safeguard.
	for _, want := range []string{"ETSI_ALL=1", "ETSI_ALL_VERSIONS=1"} {
		if got := etsiScopeEnv(""); !slices.Contains(got, want) {
			t.Errorf("the script environment for an unset scope is %v, missing %q", got, want)
		}
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
	for _, want := range []string{"ETSI_ALL=", "ETSI_ALL_VERSIONS=", "ETSI_SPECS="} {
		if got := etsiScopeEnv(ScopeLISuite); !slices.Contains(got, want) {
			t.Errorf("li-suite passes env %v, which does not clear %q", got, want)
		}
	}
}

// NO SCOPE MAY LEAVE A WORK-LIST VARIABLE TO THE AMBIENT ENVIRONMENT.
//
// THE LIST IS READ OUT OF THE SCRIPT, not typed here, because typing it is how
// this defect keeps coming back. The first version of this test named the three
// scope variables and passed while scripts/etsi-fetch.sh went on reading
// ETSI_INCLUDE_3GPP — which adds ETSI's republications of 3GPP specs — and
// ETSI_TYPE_DIRS, which decides which archives are scanned at all. Two runs with
// the same etsi_scope downloaded different corpora behind the same determinant,
// and the second would skip on a corpus the first never built.
//
// Reading the script means the next variable someone adds to the enumeration
// fails here on the day it is added, rather than the day someone exports it.
func TestEveryScopeSpeaksForEveryWorkListVariable(t *testing.T) {
	read := workListVarsOf(t, "scripts/etsi-fetch.sh")
	if len(read) < 3 {
		t.Fatalf("only %d work-list variable(s) found in the script — the reader is broken, "+
			"and a broken reader makes this test pass for the wrong reason: %v", len(read), read)
	}
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

// workListVarsOf returns every ETSI_* variable the script feeds into the
// enumeration that builds the work list — the lines that append to disc_args.
//
// Deliberately NOT every ETSI_* in the file: ETSI_JOBS sets the worker count and
// ETSI_CONVERT the output directory, and neither changes WHICH deliverables are
// fetched. A determinant covers what would make the step produce something
// different, not everything it reads.
func workListVarsOf(t *testing.T, rel string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("cannot read %s, so this test cannot know what the script reads: %v", rel, err)
	}
	re := regexp.MustCompile(`\$\{(ETSI_[A-Z0-9_]+):-\}`)
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, "disc_args+=") {
			continue
		}
		for _, m := range re.FindAllStringSubmatch(line, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1]+"=")
			}
		}
	}
	sort.Strings(out)
	return out
}
