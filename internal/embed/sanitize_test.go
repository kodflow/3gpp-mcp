package embed

import "testing"

// TestSanitizeForTokenizer locks the normalisation that defuses the sugarme
// Metaspace panic and strips conversion noise: PUA glyphs dropped, whitespace
// runs collapsed, NBSP folded, edges trimmed. Model-free (no -tags onnx).
func TestSanitizeForTokenizer(t *testing.T) {
	pua := string(rune(0xF0AF)) // a real offender seen in TS 21.x table clauses
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"plain", "hello world", "hello world"},
		{"collapse tabs", "a\t\t\t\tb", "a b"},
		{"collapse newlines", "a\n\n\nb", "a b"},
		{"nbsp folded", "a b", "a b"},
		{"pua dropped", "a" + pua + pua + "b", "a b"},
		{"pua between words", "x" + pua + "y", "x y"},
		{"edges trimmed", "  \t hi \n ", "hi"},
		{"all noise", "\t\t" + pua + "\n ", ""},
		{"mixed", "fig A.\n\t\t\t\tCR" + pua + "cat", "fig A. CR cat"},
	}
	for _, c := range cases {
		if got := sanitizeForTokenizer(c.in); got != c.want {
			t.Errorf("%s: sanitize(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestIsPUA(t *testing.T) {
	for _, r := range []rune{0xE000, 0xF0AE, 0xF0AF, 0xF8FF, 0xF0000, 0x10FFFD} {
		if !isPUA(r) {
			t.Errorf("U+%04X should be PUA", r)
		}
	}
	for _, r := range []rune{'a', 'Z', '9', 0xDFFF, 0xF900, 0x20AC} {
		if isPUA(r) {
			t.Errorf("U+%04X should NOT be PUA", r)
		}
	}
}
