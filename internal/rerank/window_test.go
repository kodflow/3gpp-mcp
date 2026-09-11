package rerank

import "testing"

// windowPrefix cuts only at a word end — an ASCII non-space byte, then an ASCII
// space — at or past n, and returns the input whole when there is none.
func TestWindowPrefixCutsOnlyAtAWordEnd(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
		cut  bool
	}{
		{"alpha beta gamma", 3, "alpha", true},      // the first word end at or past n
		{"alpha beta gamma", 5, "alpha", true},      // the space AT n
		{"alpha beta gamma", 6, "alpha beta", true}, // n inside a word: to its end
		{"alpha  beta gamma", 6, "alpha  beta", true},
		{"alpha beta", 20, "alpha beta", false},       // shorter than n
		{"alpha\nbeta gamma", 3, "alpha\nbeta", true}, // a newline is not a cut
		{"alpha\tbeta gamma", 3, "alpha\tbeta", true},
		{"été été", 1, "été été", false}, // é before the space: not ASCII
		{"été x été", 1, "été x", true},
		{"登録手順登録手順", 2, "登録手順登録手順", false}, // no ASCII space at all
		{"a b", 0, "a b", false},
	} {
		got, cut := windowPrefix(c.in, c.n)
		if got != c.want || cut != c.cut {
			t.Errorf("windowPrefix(%q, %d) = %q, %v; want %q, %v", c.in, c.n, got, cut, c.want, c.cut)
		}
	}
}

func TestEndsInSpace(t *testing.T) {
	for in, want := range map[string]bool{
		"Foreword\n": true, "a ": true, "a ": true, "a": false, "": false, "é": false,
	} {
		if got := endsInSpace(in); got != want {
			t.Errorf("endsInSpace(%q) = %v, want %v", in, got, want)
		}
	}
}
