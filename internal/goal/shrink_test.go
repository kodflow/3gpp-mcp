package goal

import (
	"strings"
	"testing"
)

// TestAMergeThatWouldLoseTheCorpusIsRefused.
//
// The numbers are the ones measured on the machine that published: a corpus of
// 20 163 spec versions whose source tree had been pruned to 1 410 converted
// files. Re-running the acquisition chain there would have folded shards built
// from 7% of the sources into the live corpus, and every gate afterwards would
// have measured the result against itself and passed.
func TestAMergeThatWouldLoseTheCorpusIsRefused(t *testing.T) {
	err := shrinkVerdict(20163, 1410, "data/3gpp.duckdb.new", false)
	if err == nil {
		t.Fatal("a merge that would drop 93% of the corpus was allowed to publish")
	}
	for _, want := range []string{"20163", "1410", "refusing to publish", corpusShrinkOverride} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q, so nobody can act on it:\n%s", want, err)
		}
	}
	// The operator must be told where the evidence is, not just that it failed.
	if !strings.Contains(err.Error(), "data/3gpp.duckdb.new") {
		t.Errorf("the refusal does not say where the merged corpus was left:\n%s", err)
	}
}

func TestTheShrinkGuardAllowsHonestMerges(t *testing.T) {
	cases := []struct {
		name          string
		before, after int
	}{
		{"a merge that adds versions", 20163, 20400},
		{"a merge that changes nothing", 20163, 20163},
		{"a withdrawal small enough to be editorial", 20163, 20000},
		{"a first build, with no base to lose", 0, 1410},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := shrinkVerdict(tc.before, tc.after, "tmp", false); err != nil {
				t.Errorf("%d -> %d was refused: %v", tc.before, tc.after, err)
			}
		})
	}
}

// TestTheShrinkOverrideIsHonoured: the guard must be escapable, or the first
// legitimate shrink turns it into something people delete rather than set.
func TestTheShrinkOverrideIsHonoured(t *testing.T) {
	if err := shrinkVerdict(20163, 1410, "tmp", true); err != nil {
		t.Fatalf("the documented override did not let a deliberate shrink through: %v", err)
	}
}

// TestTheShrinkThresholdIsWhereItClaimsToBe pins the 1% boundary, because a
// tolerance nobody can state precisely is a tolerance that drifts.
func TestTheShrinkThresholdIsWhereItClaimsToBe(t *testing.T) {
	// Exactly 99% of 10 000 is allowed; one version below it is not.
	if err := shrinkVerdict(10000, 9900, "tmp", false); err != nil {
		t.Errorf("a 1.0%% loss was refused, but the documented tolerance is 1%%: %v", err)
	}
	if err := shrinkVerdict(10000, 9899, "tmp", false); err == nil {
		t.Error("a loss beyond the documented 1% tolerance was allowed")
	}
}

func TestSpecVersionsIsReadFromTheCounterBlock(t *testing.T) {
	// dbcount emits several counters and spec_versions is not the only one whose
	// name starts with "spec". Prefix matching must not pick up a neighbour.
	out := "spec_versions=20163\napi_operations=8562\nembedding_model=38067f8c6efe\n"
	got, err := parseSpecVersions(out)
	if err != nil {
		t.Fatal(err)
	}
	if got != 20163 {
		t.Errorf("read %d, want 20163", got)
	}
	if _, err := parseSpecVersions("api_operations=8562\n"); err == nil {
		t.Error("a dbcount output with no spec_versions was accepted as a count")
	}
	if _, err := parseSpecVersions("spec_versions=not-a-number\n"); err == nil {
		t.Error("an unreadable counter was accepted as a count")
	}
}
