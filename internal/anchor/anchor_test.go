package anchor

import "testing"

// The highest version per (spec, release) — numerically, not as strings: 3GPP
// minors reach two digits (22.261 is at 19.14.0), and an anchor that kept
// "19.9.0" over "19.14.0" would call the corpus older than it is.
func TestAddKeepsTheNumericallyHighestVersion(t *testing.T) {
	ix := Index{}
	for _, v := range []string{"19.9.0", "19.14.0", "19.2.0"} {
		ix.Add("22.261", "Rel-19", v)
	}
	ix.Add("22.261", "Rel-18", "18.5.0")
	if ix["22.261|Rel-19"] != "19.14.0" || ix["22.261|Rel-18"] != "18.5.0" || len(ix) != 2 {
		t.Fatalf("got %v", ix)
	}
}

// THE FOLD'S ORDER, NOT A LOOK-ALIKE. rust/identity cmp_ver compares the first
// three components, each parsed whole as an i64, garbage as 0. Every row below is
// that function's answer, read off its code (`split('.').take(3)`,
// `p.parse().unwrap_or(0)`); the last group is where a leading-digits reading —
// cmd/anchorcheck's — would answer differently, and would have written a
// different anchor than the fold for the same corpus.
func TestCmpVerIsTheFoldsOrder(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"2.10.0", "2.9.0", 1},
		{"19.14.0", "19.9.0", 1},
		{"18.5.1", "18.5.0", 1},
		{"19.0.0", "20.0.0", -1},
		{"19.1", "19.1.0", 0},
		{"", "0.0.0", 0},
		{"1.0.0", "", 1},
		{"-1.0.0", "0.0.0", -1}, // i64 parse takes the sign
		{"+1.0.0", "1.0.0", 0},
		{"99999999999999999999.0.0", "0.0.0", 0}, // overflow is garbage: 0
		// Where the fold and a leading-digits reading part:
		{"1.2.3.4", "1.2.3", 0},   // the fourth component is never looked at
		{"1.2.3.9", "1.2.3.1", 0}, //   … in either argument
		{"19x.0.0", "0.0.0", 0},   // a component is parsed whole: "19x" is 0, not 19
		{"19.1a.0", "19.0.0", 0},
		{" 1.0.0", "0.0.0", 0}, // no trimming
	} {
		if got := CmpVer(c.a, c.b); got != c.want {
			t.Errorf("CmpVer(%q, %q) = %d, want %d (the fold's cmp_ver)", c.a, c.b, got, c.want)
		}
		if got := CmpVer(c.b, c.a); got != -c.want {
			t.Errorf("CmpVer(%q, %q) = %d, want %d (antisymmetry)", c.b, c.a, got, -c.want)
		}
	}
	// On a tie the fold keeps the FIRST row it read (it replaces only on Greater);
	// so does Add. (No key of the real corpus has two versions that tie — measured.)
	ix := Index{}
	ix.Add("x", "R", "1.2.3.4")
	ix.Add("x", "R", "1.2.3")
	if ix["x|R"] != "1.2.3.4" {
		t.Errorf("a tie replaced the first row read: %q", ix["x|R"])
	}
}

// THE BYTES ARE THE FOLD'S: sorted keys, one-space indent, no trailing newline.
// The anchor is an Input of discover (size + mtime), and the byte-identity with
// merge --index-out is what the 554 235-byte measurement on the real corpus rests on.
func TestMarshalIsTheFoldsFormat(t *testing.T) {
	ix := Index{}
	ix.Add("29.518", "Rel-19", "19.0.0")
	ix.Add("23.501", "Rel-19", "19.5.0")
	b, err := ix.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n \"23.501|Rel-19\": \"19.5.0\",\n \"29.518|Rel-19\": \"19.0.0\"\n}"
	if string(b) != want {
		t.Fatalf("got %q\nwant %q", b, want)
	}
	back, err := Parse(b)
	if err != nil || len(back) != 2 || back["23.501|Rel-19"] != "19.5.0" {
		t.Fatalf("round trip: %v %v", back, err)
	}
}

// Compare names both directions separately, because they cost different things:
// behind/missing is re-work, ahead/extra is a hole discover will never revisit.
func TestCompareSeparatesReworkFromHoles(t *testing.T) {
	want := Index{"a|R": "1.2.0", "b|R": "1.0.0", "c|R": "2.0.0", "d|R": "1.0.0"}
	have := Index{"a|R": "1.1.0", "b|R": "1.10.0", "c|R": "2.0.0", "z|R": "9.0.0"}
	d := Compare(have, want)
	if d != (Drift{Behind: 1, Missing: 1, Ahead: 1, Extra: 1}) {
		t.Fatalf("got %+v", d)
	}
	if !d.OverClaims() || d.Zero() {
		t.Errorf("OverClaims=%v Zero=%v", d.OverClaims(), d.Zero())
	}
	if d := Compare(want, want); !d.Zero() || d.OverClaims() {
		t.Errorf("an anchor compared with itself drifts: %+v", d)
	}
	if d := Compare(Index{"a|R": "1.0.0"}, Index{"a|R": "1.1.0"}); d.OverClaims() {
		t.Errorf("an anchor that is only BEHIND was called an over-claim: %+v", d)
	}
}
