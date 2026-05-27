package model

import "testing"

func TestReleaseOrdinal(t *testing.T) {
	cases := []struct {
		rel  string
		want int
		ok   bool
	}{
		{"Rel-99", 3, true}, // Rel-99 is version major 3, not 99
		{"Rel-4", 4, true},
		{"Rel-15", 15, true},
		{"Rel-18", 18, true},
		{"Rel-20", 20, true},
		{"Rel-3", 0, false},  // majors 0/1/2/3-as-"Rel-3" are pre-Rel-99 drafts
		{"Rel-0", 0, false},  // draft
		{"Rel-", 0, false},   // malformed
		{"18", 0, false},     // missing prefix
		{"", 0, false},       // empty
		{"Rel-xx", 0, false}, // non-numeric
	}
	for _, c := range cases {
		got, ok := ReleaseOrdinal(c.rel)
		if got != c.want || ok != c.ok {
			t.Errorf("ReleaseOrdinal(%q) = (%d,%v), want (%d,%v)", c.rel, got, ok, c.want, c.ok)
		}
	}
	// Round-trips with ReleaseFromMajor for real releases.
	for _, m := range []int{3, 4, 15, 18, 20} {
		if o, ok := ReleaseOrdinal(ReleaseFromMajor(m)); !ok || o != m {
			t.Errorf("round-trip major %d: ReleaseOrdinal(%q) = (%d,%v)", m, ReleaseFromMajor(m), o, ok)
		}
	}
}
