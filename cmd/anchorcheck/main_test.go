package main

import "testing"

// TestCmpVerOrdersNumerically pins the trap that string comparison falls into:
// "2.10.0" is NEWER than "2.9.0", and 3GPP reaches double-digit minors routinely
// (22.261 is at 19.14.0). A lexicographic anchor comparison would call the newer
// version older and silently skip the spec.
func TestCmpVerOrdersNumerically(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2.10.0", "2.9.0", 1},
		{"19.14.0", "19.9.0", 1},
		{"19.1.0", "19.1.0", 0},
		{"18.5.1", "18.5.0", 1},
		{"19.0.0", "20.0.0", -1},
		{"19.1", "19.1.0", 0}, // a missing component is zero, not "smaller"
		{"", "0.0.0", 0},      // an empty anchor equals nothing indexed
		{"1.0.0", "", 1},      // ... and any real version beats it
		{"19.x", "19.0.0", 0}, // malformed tail degrades, never panics
	}
	for _, c := range cases {
		if got := CmpVer(c.a, c.b); got != c.want {
			t.Errorf("CmpVer(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestClassifySeparatesFilingArtefactFromHole is the distinction the whole tool
// turns on. 3GPP lists a spec's Rel-N entry at the Rel-(N-1) version, so "no
// clause under this release" is usually bookkeeping. Reporting those as holes
// would bury the 56 real ones in 61 false alarms; missing the distinction in the
// other direction would hide them entirely.
func TestClassifySeparatesFilingArtefactFromHole(t *testing.T) {
	c := &Corpus{
		ByClause: map[string]string{
			"23.501|Rel-19": "19.5.0",
			"24.501|Rel-19": "19.1.0",
		},
		SpecVersionSeen: map[string]bool{
			"23.501@19.5.0": true,
			"24.501@19.1.0": true,
		},
	}
	cases := []struct {
		name, key, ver string
		want           Verdict
	}{
		{"backed by indexed text", "23.501|Rel-19", "19.5.0", Indexed},
		{"anchor behind the DB is fine", "23.501|Rel-19", "19.4.0", Indexed},
		{"anchor ahead of the DB is a lie", "23.501|Rel-19", "19.6.0", OverClaim},
		{"same spec+version filed under another release", "24.501|Rel-20", "19.1.0", NonContent},
		{"nothing indexed anywhere", "29.502|Rel-20", "19.5.0", MissingContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.key, tc.ver, c); got != tc.want {
				t.Errorf("Classify(%q,%q)=%v want %v", tc.key, tc.ver, name(got), name(tc.want))
			}
		})
	}
}

// TestClassifyTreatsDoubleDigitMinorsCorrectly guards the interaction between the
// two: an over-claim must not be reported just because 19.14.0 sorts oddly.
func TestClassifyTreatsDoubleDigitMinorsCorrectly(t *testing.T) {
	c := &Corpus{
		ByClause:        map[string]string{"22.261|Rel-19": "19.14.0"},
		SpecVersionSeen: map[string]bool{"22.261@19.14.0": true},
	}
	if got := Classify("22.261|Rel-19", "19.9.0", c); got != Indexed {
		t.Fatalf("anchor 19.9.0 vs indexed 19.14.0 classified %v, want Indexed", name(got))
	}
}
