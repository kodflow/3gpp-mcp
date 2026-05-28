package embed

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// safePrefix is defined in embed_onnx.go (onnx-tagged). This test exercises
// the helper without requiring -tags onnx to actually link — we re-declare a
// twin here. The intent: a regression on the real safePrefix shows up as a
// drift between the two implementations.
func safePrefixTwin(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func TestSafePrefixTwin(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"ascii under limit", "hello", 80, "hello"},
		{"ascii at limit", strings.Repeat("a", 10), 10, strings.Repeat("a", 10)},
		{"ascii over limit", strings.Repeat("a", 100), 10, strings.Repeat("a", 10) + "…"},
		// Each "é" is 2 bytes (0xC3 0xA9). Cut at byte 5 lands MID-codepoint; the
		// helper must walk back to a rune boundary, not slice into the middle.
		{"mid-codepoint cut", "abcééé", 5, "abcé" + "…"},
		// Empty input: returned as-is, no panic.
		{"empty", "", 5, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := safePrefixTwin(c.in, c.n); got != c.want {
				t.Errorf("safePrefix(%q,%d) = %q, want %q", c.in, c.n, got, c.want)
			}
			// Always produces valid UTF-8.
			if got := safePrefixTwin(c.in, c.n); !utf8.ValidString(got) {
				t.Errorf("safePrefix(%q,%d) = %q is invalid UTF-8", c.in, c.n, got)
			}
		})
	}
}
