package goal

import (
	"sort"
	"strings"
	"testing"
)

// sharedSteps handle BOTH halves inside one step, so they belong to neither arm.
//
// They are listed rather than detected because "does this step touch both
// corpora" is a fact about its body, not about its name, and a wrong guess here
// would quietly excuse a real asymmetry.
var sharedSteps = map[string]string{
	"toolchain":      "tool: no corpus",
	"build-go":       "tool: no corpus",
	"test":           "tool: no corpus",
	"build-rust":     "tool: no corpus",
	"build-embedder": "tool: no corpus",
	"build-sparse":   "tool: no corpus",
	"build-serve":    "tool: no corpus",
	"seed":           "bootstraps the 3GPP corpus only, and declines once it exists",
	"compact":        "rewrites BOTH corpora in one step",
	"validate":       "one contract over both halves (--require-etsi)",
	"smoke":          "starts the server with both halves attached",
	"publish":        "one image carrying both halves",
}

// armExceptions are stages one arm has and the other legitimately cannot, with
// the reason. An entry here is a claim that has to stay true.
var armExceptions = map[string]string{
	"merge": "3GPP ingests into per-series SHARDS and folds them; the ETSI ingest " +
		"writes one database in a single pass, so there is nothing to fold. " +
		"`ingest --etsi` IS the publish on that side (rust/ingest/src/main.rs).",
}

// TestTheTwoArmsExposeTheSameStages is the table this repository is asked to be
// able to draw: 3GPP and ETSI, side by side, one row per stage.
//
// It was not drawable. The stages had drifted apart in NAME as well as in
// substance — the ETSI ingest was called `corpus-etsi` while its 3GPP twin was
// `ingest`, so a reader lining the two arms up had to know that those two words
// meant the same thing. The script it runs had already been renamed to
// scripts/etsi-ingest.sh (PR #293); only the step name lagged.
//
// Asserting the names is not cosmetics. The pairing is what makes the arms
// COMPARABLE — which pair costs what, which pair could ever overlap — and a
// pairing that exists only in a comment is one nobody can check.
func TestTheTwoArmsExposeTheSameStages(t *testing.T) {
	const suffix = "-etsi"
	gpp, etsi := map[string]bool{}, map[string]bool{}
	for _, s := range Pipeline() {
		if _, shared := sharedSteps[s.Name]; shared {
			continue
		}
		if stage, ok := strings.CutSuffix(s.Name, suffix); ok {
			etsi[stage] = true
			continue
		}
		gpp[s.Name] = true
	}

	if len(gpp) == 0 || len(etsi) == 0 {
		t.Fatalf("one arm is empty: 3GPP=%v ETSI=%v — the partition is wrong, not the pipeline",
			keys(gpp), keys(etsi))
	}

	for stage := range gpp {
		if etsi[stage] {
			continue
		}
		if why, allowed := armExceptions[stage]; allowed {
			t.Logf("3GPP-only stage %q, by declaration: %s", stage, why)
			continue
		}
		t.Errorf("the 3GPP arm has stage %q and the ETSI arm has no %q. Either add it, "+
			"or declare it in armExceptions WITH the reason — an asymmetry nobody "+
			"wrote down is how `enrich` stayed missing from the ETSI half", stage, stage+suffix)
	}
	for stage := range etsi {
		if gpp[stage] {
			continue
		}
		if why, allowed := armExceptions[stage]; allowed {
			t.Logf("ETSI-only stage %q, by declaration: %s", stage, why)
			continue
		}
		t.Errorf("the ETSI arm has stage %q and the 3GPP arm has no %q", stage, stage)
	}
}

// Every declared exception must name a stage that actually exists on one arm.
// A stale entry is worse than none: it silently excuses a stage that has since
// been added or removed.
func TestNoArmExceptionOutlivesItsStage(t *testing.T) {
	names := map[string]bool{}
	for _, s := range Pipeline() {
		names[s.Name] = true
	}
	for stage, why := range armExceptions {
		if !names[stage] && !names[stage+"-etsi"] {
			t.Errorf("armExceptions still excuses %q (%s), and no such stage exists on either arm",
				stage, why)
		}
	}
}

// The ETSI ingest is named for what it DOES, like its twin. This pins the rename
// rather than trusting it to survive the next edit.
func TestTheETSIIngestIsCalledIngest(t *testing.T) {
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

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
