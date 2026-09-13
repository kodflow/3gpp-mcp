package goal

import (
	"slices"
	"strings"
	"testing"
)

// A STEP THAT WRITES A FILE ANOTHER STEP REQUIRES MUST DECLARE WHAT WRITES IT.
//
// This is the invariant PR #352 broke and the publish run of 2026-09-12 23:34
// exposed. That PR gave `discover` a new output — catalog-specs.tsv, the catalogue
// projection — and made it a MANDATORY input of `enrich`, which now refuses to
// start without it. `discover` declared only `rust/discover` and
// `scripts/lib/discover.sh`, while the Go that writes all three of its files lives
// in internal/goal/pipeline_steps.go. So the step's fingerprint did not move, and
// on the first run after the merge it SKIPPED.
//
// It happened to be harmless there, and only by luck: the from-zero build had
// invalidated the entire ledger an hour earlier and produced the file. On a machine
// that had not, `discover` skips, the projection is absent, and `enrich` stops the
// build on an error written for a case that should have been unreachable.
//
// The general rule is not checkable from here — "which file does this Run write"
// is not a question a test can ask of a closure. What IS checkable is the specific
// pairing that cost this: a step whose declared Outputs another step declares as
// Inputs must declare the implementation file its Run is written in. Pinned by
// name, because that is the form the defect took.
func TestDiscoverDeclaresTheGoThatWritesItsOutputs(t *testing.T) {
	byName := map[string]*Step{}
	for _, s := range Pipeline() {
		byName[s.Name] = s
	}
	d := byName["discover"]
	if d == nil {
		t.Fatal("no discover step")
	}
	for _, want := range []string{
		"internal/goal/pipeline_steps.go", // runDiscover: writes all three outputs
		"internal/goal/pipeline.go",       // the step itself: Inputs, Outputs, Validate, Extra
		"internal/goal/freshness.go",      // the determinant that decides whether it looks at all
	} {
		if !slices.Contains(d.Impl, want) {
			t.Errorf("discover does not declare %q as an implementation file: a change to what it "+
				"WRITES would not move its fingerprint, it would skip, and `enrich` would stop "+
				"the build on a missing catalogue projection (Impl: %v)", want, d.Impl)
		}
	}
	// And the tests in those packages must NOT count, or a test-only commit replays
	// the enumeration and everything its outputs reach.
	if !d.ExcludeTests {
		t.Error("discover counts _test.go files toward its fingerprint: it runs binaries, it does " +
			"not compile them, and a test-only commit would replay it")
	}
}

// THE PROJECTION IS DECLARED AT BOTH ENDS, and by the same name. `enrich` requires
// the file `discover` promises; a rename on one side and not the other is a build
// that fails at the consumer with the producer reporting success.
func TestTheCatalogueProjectionIsTheSameFileAtBothEnds(t *testing.T) {
	c, _ := newTestCtx(t)
	byName := map[string]*Step{}
	for _, s := range Pipeline() {
		byName[s.Name] = s
	}

	produced := byName["discover"].Outputs(c)
	if !slices.ContainsFunc(produced, func(p string) bool {
		return strings.HasSuffix(p, catalogProjection)
	}) {
		t.Fatalf("discover does not declare %s among its outputs: %v", catalogProjection, produced)
	}

	consumed, err := byName["enrich"].Inputs(c)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(consumed, func(p string) bool {
		return strings.HasSuffix(p, catalogProjection)
	}) {
		t.Errorf("enrich does not declare %s among its inputs: the overlay would write from "+
			"something this step does not watch", catalogProjection)
	}

	// And the report itself is gone from enrich's inputs — the whole point of #352.
	// 3gpp.org re-nonces every response, so declaring it makes this step dirty on a
	// clock and replays the entire 3GPP write side behind it.
	for _, p := range consumed {
		if strings.HasSuffix(p, "status-report.htm") {
			t.Errorf("enrich declares the raw status report (%s) again: it cannot be equal to "+
				"itself twice, so the corpus would replay every 6 h to publish the same digest", p)
		}
	}
}
