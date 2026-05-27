package ingest

import "testing"

func TestVerLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"18.6.0", "19.6.0", true},
		{"19.6.0", "19.15.0", true}, // numeric, not lexical (6 < 15)
		{"19.15.0", "19.6.0", false},
		{"17.20.0", "17.20.0", false},
	}
	for _, c := range cases {
		if got := verLess(c.a, c.b); got != c.want {
			t.Errorf("verLess(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestSeedEvolutions(t *testing.T) {
	evos := seedEvolutions()
	if len(evos) < 10 {
		t.Errorf("expected a substantial evolution seed, got %d", len(evos))
	}
	var mmeToAMF bool
	for _, e := range evos {
		if e.FromTerm == "MME" && e.ToTerm == "AMF" && e.EvolutionType == "SPLIT" {
			mmeToAMF = true
		}
		if e.Confidence <= 0 || e.Confidence > 1 {
			t.Errorf("bad confidence on %s->%s: %v", e.FromTerm, e.ToTerm, e.Confidence)
		}
	}
	if !mmeToAMF {
		t.Error("seed missing the canonical MME->AMF SPLIT edge")
	}
}
