package goal

import (
	"slices"
	"sort"
	"strings"
	"testing"
)

// armShared records the steps that are legitimately not per corpus, each with the
// reason it is not. This map is the ONLY place an exception can be recorded, so
// adding one is a decision someone typed and a reviewer can see.
var armShared = map[string]string{
	"toolchain":       "verifies the machine, not a corpus",
	"build-go":        "builds the binaries both arms run",
	"build-rust":      "builds the binaries both arms run",
	"build-embedder":  "builds the binaries both arms run",
	"build-sparse":    "builds the binaries both arms run",
	"build-serve":     "builds the binaries both arms run",
	"test":            "runs the suite, not a corpus",
	"seed":            "applies the two curated 3GPP seeds; the ETSI vocabulary is MINED, by enrich-etsi",
	"merge":           "folds the 3GPP shards; the ETSI ingest writes one database directly",
	"smoke":           "starts ONE server over BOTH stores — splitting it would prove each half serves and leave the federation proven by neither",
	"publish":         "pushes ONE image carrying both corpora",
	"discover-etsi":   "twin of discover",
	"fetch-etsi":      "twin of fetch",
	"ingest-etsi":     "twin of ingest",
	"embed-etsi":      "twin of embed",
	"enrich-etsi":     "twin of enrich",
	"paragraphs-etsi": "twin of paragraphs",
	"sparse-etsi":     "twin of sparse",
	"compact-etsi":    "twin of compact",
	"index-etsi":      "twin of index",
	"validate-etsi":   "twin of validate",
}

// THE TWO ARMS MUST BE THE SAME LIST TWICE.
//
// Every hole this pipeline has had in the ETSI half was a MISSING TWIN, and not
// one of them was found by something failing. They were found by reading the step
// list and noticing a gap in a column:
//
//	enrich-etsi      rust/ingest/src/bin/ingest_glossary.rs was written, tested and
//	                 built by build-rust, and run by NO step. The 4 941 acronyms the
//	                 shipped ETSI half carried had been written by hand, once, in a
//	                 session nobody can replay. A fresh clone built an ETSI corpus
//	                 with an EMPTY glossary and reported success.
//	validate-etsi    `validate` ran the whole contract on 3gpp.duckdb and judged the
//	                 ETSI half by one composite flag. An ETSI FTS index that failed
//	                 to build, or a sparse layer that came out empty, could not fail
//	                 a gate, because no gate looked.
//	compact-etsi     one shared `compact` declared paragraphs, paragraphs-etsi and
//	                 sparse-etsi — so the ETSI sparse import made it dirty and the
//	                 3GPP one, which writes the same rows into the same table, did
//	                 not. Nothing detected it, because the step rewrote both files
//	                 whenever it ran for any reason at all.
//	ingest-etsi      was called `corpus-etsi`: the one step in either arm whose name
//	                 did not pair. A name that does not pair is a step nobody looks
//	                 for when they ask whether both halves get the same treatment.
//
// So the pairing is the invariant, and this test is what makes it one. Adding a
// data step to either arm without its twin fails here, at compile-and-test time,
// instead of shipping a corpus that is quietly worse than the other one.
func TestTheTwoArmsRunTheSameSteps(t *testing.T) {
	names := map[string]bool{}
	for _, s := range Pipeline() {
		names[s.Name] = true
	}

	shared := armShared

	// Every 3GPP data step must have an ETSI twin.
	var missing []string
	for name := range names {
		if strings.HasSuffix(name, "-etsi") {
			continue
		}
		if _, ok := shared[name]; ok {
			continue
		}
		if !names[name+"-etsi"] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	for _, name := range missing {
		t.Errorf("the 3GPP arm has %q and the ETSI arm has no %q: either add the twin, or "+
			"record in this test's `shared` map why the ETSI half does not need one. A step "+
			"the ETSI corpus silently goes without is the defect this test exists to catch",
			name, name+"-etsi")
	}

	// And no ETSI step may exist without its 3GPP original — an ETSI-only step is
	// the same divergence seen from the other side.
	for name := range names {
		base, ok := strings.CutSuffix(name, "-etsi")
		if !ok {
			continue
		}
		if !names[base] {
			t.Errorf("the ETSI arm has %q and the 3GPP arm has no %q", name, base)
		}
	}
}

// THE TWINS MUST BE TWINS, not merely same-named. A pair whose dependencies point at
// different things in the DAG is two steps wearing one name, which is how
// `compact` came to declare the ETSI sparse import and not the 3GPP one.
//
// The rule: within a twinned pair, every dependency of the ETSI step must be
// either the -etsi twin of the 3GPP step's corresponding dependency, or a step the
// `shared` list above says is legitimately shared (a Tool, or an arm-specific
// producer). Expressed the way it is actually checkable: the ETSI step must not
// depend on a 3GPP DATA step that has a twin.
func TestNoArmDependsOnTheOtherArmsDataSteps(t *testing.T) {
	byName := map[string]*Step{}
	for _, s := range Pipeline() {
		byName[s.Name] = s
	}

	// validate is the ONE deliberate exception, and its contract is the reason: it
	// carries --require-etsi, the single check about the PAIR rather than about a
	// corpus, so it opens the other half and asserts that half's HNSW is frozen.
	// Build 24 (2026-09-07) failed exactly because that edge was missing.
	crossArm := map[string][]string{"validate": {"index-etsi"}}

	for name, s := range byName {
		base, ok := strings.CutSuffix(name, "-etsi")
		if !ok || byName[base] == nil {
			continue
		}
		for _, d := range append(append([]string{}, s.Deps...), s.AnyDeps...) {
			if strings.HasSuffix(d, "-etsi") {
				continue
			}
			dep := byName[d]
			if dep == nil {
				t.Fatalf("%s depends on %q, which is not in the pipeline", name, d)
			}
			if dep.Tool {
				continue // a compiler output is not another arm's data
			}
			if byName[d+"-etsi"] != nil {
				t.Errorf("%s depends on the 3GPP data step %q, whose twin %q exists: the ETSI "+
					"arm would replay whenever the 3GPP corpus moved, and the two steps are "+
					"not twins at all", name, d, d+"-etsi")
			}
		}
	}

	// And the same rule from the other side, minus the recorded exception.
	for name, s := range byName {
		if strings.HasSuffix(name, "-etsi") || byName[name+"-etsi"] == nil {
			continue
		}
		for _, d := range append(append([]string{}, s.Deps...), s.AnyDeps...) {
			if !strings.HasSuffix(d, "-etsi") || slices.Contains(crossArm[name], d) {
				continue
			}
			t.Errorf("%s depends on the ETSI data step %q with no reason recorded in crossArm",
				name, d)
		}
	}
}

// The contract that gates each corpus must come from the arm's own config entry.
// Reading "contract_flags" on both arms is how the ETSI half would silently be
// held to the 3GPP contract — including --require-etsi, which would then point the
// ETSI corpus at itself and pass by construction.
func TestEachArmReadsItsOwnContract(t *testing.T) {
	if got := corpus3GPP().ContractKey; got != "contract_flags" {
		t.Errorf("the 3GPP arm reads %q", got)
	}
	if got := corpusETSI().ContractKey; got != "contract_flags_etsi" {
		t.Errorf("the ETSI arm reads %q", got)
	}
	if corpus3GPP().ContractKey == corpusETSI().ContractKey {
		t.Error("both arms read the same contract entry, so one of them is not being checked " +
			"against the flags scripts/data-contract.sh emits for it")
	}
}

// validateArgs must validate the corpus it was handed. It used to hardcode
// data/3gpp.duckdb, which is exactly what made a second gate impossible.
func TestValidateArgsPointAtTheArmsOwnCorpus(t *testing.T) {
	c, _ := newTestCtx(t)
	c.Config["contract_flags"] = "--require-fts"
	c.Config["contract_flags_etsi"] = "--require-fts"

	for _, tc := range []struct {
		target corpusTarget
		want   string
	}{{corpus3GPP(), "3gpp.duckdb"}, {corpusETSI(), "etsi.duckdb"}} {
		args := validateArgs(c, tc.target)
		i := slices.Index(args, "--db")
		if i < 0 || i+1 >= len(args) {
			t.Fatalf("validate%s passes no --db: %v", tc.target.Suffix, args)
		}
		if !strings.HasSuffix(args[i+1], tc.want) {
			t.Errorf("validate%s validates %s, want %s", tc.target.Suffix, args[i+1], tc.want)
		}
	}
}

// A RELEASE FLOOR MUST NEVER REACH THE ETSI ARM, one gate after the place that
// rule is already written down. clauses_needing_embedding skips any clause whose
// release has no ordinal once a floor is set, and an ETSI release is the constant
// "ETSI" — so --require-embed-complete under a floor selects ZERO clauses and the
// strongest check in the contract reports [ok] over an unvectorised corpus.
func TestTheETSIContractNeverCarriesAReleaseFloor(t *testing.T) {
	c, _ := newTestCtx(t)
	c.Config["contract_flags_etsi"] = "--require-embed-complete"
	c.Config["embed_floor"] = "Rel-99"

	if args := validateArgs(c, corpusETSI()); hasFlag(args, "--embed-floor") {
		t.Errorf("the ETSI gate took a release floor, which selects zero ETSI clauses: %v", args)
	}
}

// Each compaction is determined by the writers of ITS OWN corpus. The shared step
// declared sparse-etsi and not sparse, so the 3GPP dense-space reclaim was blind to
// the 3GPP sparse import — invisible, because the step rewrote both files anyway.
func TestEachCompactionNamesItsOwnWriters(t *testing.T) {
	byName := map[string]*Step{}
	for _, s := range Pipeline() {
		byName[s.Name] = s
	}
	for _, suffix := range []string{"", "-etsi"} {
		s := byName["compact"+suffix]
		if s == nil {
			t.Fatalf("compact%s is not in the pipeline", suffix)
		}
		for _, want := range []string{"paragraphs" + suffix, "sparse" + suffix} {
			if !slices.Contains(s.Deps, want) {
				t.Errorf("compact%s does not depend on %s: a writer that leaves dead blocks "+
					"behind cannot make the compaction dirty (deps: %v)", suffix, want, s.Deps)
			}
		}
		// And NOT on the other corpus's writers. Only the ETSI arm can express
		// this: the 3GPP names are bare prefixes of the ETSI ones, so "does the
		// 3GPP compaction depend on `paragraphs-etsi`" is a question with an
		// answer, while its mirror is not a distinct string at all. The 3GPP side
		// of the rule is carried by TestNoArmDependsOnTheOtherArmsDataSteps.
		if suffix != "-etsi" {
			continue
		}
		for _, unwanted := range []string{"paragraphs", "sparse"} {
			if slices.Contains(s.Deps, unwanted) {
				t.Errorf("compact%s depends on %s, a writer of the OTHER corpus: it would "+
					"rewrite a file nothing had touched (deps: %v)", suffix, unwanted, s.Deps)
			}
		}
	}
}

// EVERY DECLARED EXCEPTION MUST NAME A STEP THAT ACTUALLY EXISTS. A stale entry is
// worse than no entry at all: `armShared` is consulted to decide whether a missing
// twin is deliberate, so an entry that outlives its step silently excuses a step
// that has since been renamed or removed — and the next one to carry that name
// inherits the excuse without anyone choosing it. `corpus-etsi` is exactly how that
// happens: rename a step and the old key keeps answering "this is fine".
func TestNoArmExceptionOutlivesItsStep(t *testing.T) {
	names := map[string]bool{}
	for _, s := range Pipeline() {
		names[s.Name] = true
	}
	for step, why := range armShared {
		if !names[step] {
			t.Errorf("armShared still excuses %q (%s), and no such step is in the pipeline: "+
				"delete the entry, or restore the step it was written for", step, why)
		}
	}
}

// THE ETSI INGEST IS NAMED FOR WHAT IT DOES, like its twin. The pairing test above
// reads names, so the rename is the thing that makes it able to see this step at
// all — under the old name `corpus-etsi` the ETSI arm's ingest paired with nothing
// and the absence looked like a deliberate exception. This pins the name rather
// than trusting it to survive the next edit.
func TestTheETSIIngestIsCalledIngestETSI(t *testing.T) {
	names := map[string]bool{}
	for _, s := range Pipeline() {
		names[s.Name] = true
	}
	if !names["ingest-etsi"] {
		t.Error("no `ingest-etsi` step: the ETSI arm's ingest must pair with `ingest` by name")
	}
	if names["corpus-etsi"] {
		t.Error("`corpus-etsi` is back: the step is the ETSI arm's INGEST, and calling it " +
			"something else is what made the two arms impossible to line up")
	}
}
