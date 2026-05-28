package embed

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSafePrefix(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"ascii under limit", "hello", 80, "hello"},
		{"ascii at limit", strings.Repeat("a", 10), 10, strings.Repeat("a", 10)},
		{"ascii over limit", strings.Repeat("a", 100), 10, strings.Repeat("a", 10) + "…"},
		// Each "é" is 2 bytes (0xC3 0xA9). A cut at byte 5 lands MID-codepoint; the
		// helper must walk back to a rune boundary, not slice into the middle.
		{"mid-codepoint cut", "abcééé", 5, "abcé" + "…"},
		{"empty", "", 5, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := safePrefix(c.in, c.n)
			if got != c.want {
				t.Errorf("safePrefix(%q,%d) = %q, want %q", c.in, c.n, got, c.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("safePrefix(%q,%d) = %q is invalid UTF-8", c.in, c.n, got)
			}
		})
	}
}

func TestSnippetHashStable(t *testing.T) {
	// Same input → same hash (correlation across log lines).
	a := snippetHash("hello world")
	b := snippetHash("hello world")
	if a != b {
		t.Fatalf("snippetHash not stable: %q vs %q", a, b)
	}
	if len(a) != 8 {
		t.Errorf("snippetHash len = %d, want 8 hex chars", len(a))
	}
	if snippetHash("hello world") == snippetHash("hello worle") {
		t.Errorf("snippetHash collides on near-identical inputs (highly unlikely)")
	}
}

func TestSafeEncodeSuccess(t *testing.T) {
	encoder := func(s string) ([]int, error) { return []int{1, 2, 3}, nil }
	ids, err := safeEncode(encoder, "anything")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(ids) != 3 {
		t.Errorf("expected 3 ids, got %d", len(ids))
	}
}

func TestSafeEncodeError(t *testing.T) {
	// A clean error must propagate untouched — the caller distinguishes errors
	// (misconfiguration) from panics (pathological input).
	sentinel := errors.New("tokenizer down")
	encoder := func(s string) ([]int, error) { return nil, sentinel }
	ids, err := safeEncode(encoder, "x")
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error to propagate, got %v", err)
	}
	if ids != nil {
		t.Errorf("expected nil ids on error, got %v", ids)
	}
}

func TestSafeEncodePanicSliceBounds(t *testing.T) {
	// Reproduces the exact panic shape we observed from sugarme/tokenizer v0.3.0
	// in CI ("slice bounds out of range") — must NOT crash the process; must
	// surface as a typed error.
	encoder := func(s string) ([]int, error) {
		// Deliberately panic the same way the upstream Metaspace pretokenizer does.
		buf := []byte("abc")
		_ = buf[5:6] //nolint:gocritic // intentional out-of-bounds for panic-recovery test
		return nil, nil
	}
	ids, err := safeEncode(encoder, "anything")
	if err == nil {
		t.Fatalf("expected an error from panic recovery, got nil")
	}
	if !strings.Contains(err.Error(), "tokenizer panic") {
		t.Errorf("error message should mention 'tokenizer panic', got %q", err.Error())
	}
	if ids != nil {
		t.Errorf("expected nil ids on panic, got %v", ids)
	}
}

func TestSafeEncodePanicArbitraryValue(t *testing.T) {
	// recover() accepts any value; the wrapper must handle non-error panics
	// (string, struct, …) without itself panicking on the format verb.
	encoder := func(s string) ([]int, error) { panic("custom string panic") }
	ids, err := safeEncode(encoder, "anything")
	if err == nil {
		t.Fatal("expected error from arbitrary panic value, got nil")
	}
	if !strings.Contains(err.Error(), "custom string panic") {
		t.Errorf("error should embed the panic value, got %q", err.Error())
	}
	if ids != nil {
		t.Errorf("expected nil ids, got %v", ids)
	}
}
