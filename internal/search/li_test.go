package search

import "testing"

func TestClassify(t *testing.T) {
	cases := map[string]Intent{
		"TS 33.128 clause 6":                 IntentSpecLookup,
		"diff between Rel-18 and Rel-19":     IntentChangelog,
		"definition of AMF":                  IntentGlossary,
		"what replaces MME":                  IntentGraph,
		"lawful interception event handling": IntentHybrid,
	}
	for q, want := range cases {
		if got := Classify(q); got != want {
			t.Errorf("Classify(%q) = %q, want %q", q, got, want)
		}
	}
}
