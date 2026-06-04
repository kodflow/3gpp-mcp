package main

import "testing"

// TestEncodeVerCode locks the inverse of corpus.sh's decode_char: each X.Y.Z
// component 0..35 → '0'..'9','a'..'z', missing components default 0, >35 fails.
func TestEncodeVerCode(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"1.0.0", "100", true},       // draft major 1 — the case the old floor dropped
		{"0.7.0", "070", true},       // pure draft
		{"1.1.1", "111", true},       // Rel-20 draft 21.802
		{"17.6.0", "h60", true},      // 17→'h'
		{"10.5.0", "a50", true},      // 10→'a'
		{"35.35.35", "zzz", true},    // upper bound of the single-char scheme
		{"3.0.0", "300", true},       // v3.x (Rel-4 mis-filed by the old major→release)
		{"19.0", "j00", true},        // 19→'j', missing patch → 0
		{"8.37.0", "083700", true},   // minor 37 > 35 → 6-digit decimal (24.229)
		{"1.62.0", "016200", true},   // minor 62 > 35 → 6-digit (30.531)
		{"36.0.0", "360000", true},   // major 36 > 35 → 6-digit (no longer dropped)
		{"99.99.99", "999999", true}, // upper bound of the 6-digit scheme
		{"100.0.0", "", false},       // ≥ 100 — outside even the 6-digit scheme
		{"x.0.0", "", false},         // non-numeric
	}
	for _, c := range cases {
		got, ok := encodeVerCode(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("encodeVerCode(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

// TestSeriesSet parses the corpus.sh-style filters into 2-digit prefixes.
func TestSeriesSet(t *testing.T) {
	if seriesSet("") != nil {
		t.Fatal("empty filter must be nil (all series)")
	}
	s := seriesSet("23 33")
	if !s["23"] || !s["33"] || len(s) != 2 {
		t.Fatalf("space filter: %v", s)
	}
	s = seriesSet("24|Rel-19,29")
	if !s["24"] || !s["29"] || len(s) != 2 {
		t.Fatalf("sub-shard|comma filter: %v", s)
	}
}
