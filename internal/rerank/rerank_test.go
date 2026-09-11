package rerank

import (
	"context"
	"testing"
)

func TestDisabled(t *testing.T) {
	var d Disabled
	if d.Enabled() {
		t.Error("Disabled.Enabled() should be false")
	}
	s, err := d.Score(context.Background(), "q", []string{"a", "b"})
	if err != nil || s != nil {
		t.Errorf("Disabled.Score = %v, %v; want nil, nil", s, err)
	}
}

func TestLexicalRanksOverlap(t *testing.T) {
	var l Lexical
	if !l.Enabled() {
		t.Fatal("Lexical should be enabled")
	}
	query := "AMF registration event over X2"
	passages := []string{
		"Change history table of contents foreword scope",      // no overlap
		"AMF registration event generation of xIRI over LI_X2", // high overlap
		"SMF PDU session establishment",                        // low overlap
	}
	scores, err := l.Score(context.Background(), query, passages)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 3 {
		t.Fatalf("want 3 scores, got %d", len(scores))
	}
	if !(scores[1] > scores[2] && scores[2] >= scores[0]) {
		t.Errorf("expected passage[1] > passage[2] >= passage[0], got %v", scores)
	}
}

// The fallback form of a passage the tokenizer panicked on: its whitespace runs
// folded to one space, none at the end, a leading run kept as one space — the
// rewrites the library's normaliser crashes performing (see forTokenizer).
func TestForTokenizerFoldsWhitespace(t *testing.T) {
	for in, want := range map[string]string{
		"Foreword\n":                      "Foreword",
		"5.1 Scope\n":                     "5.1 Scope",
		"0 1 0 1 1 5\n":                   "0 1 0 1 1 5",
		"Foreword\r\n":                    "Foreword",
		"Foreword\t \n":                   "Foreword",
		"Step Direction      Description": "Step Direction Description",
		"Scope\nbody\ttext":               "Scope body text",
		"\nForeword":                      " Foreword",
		"  a":                             " a",
		"":                                "",
		"\n":                              "",
		"AMF":                             "AMF",
	} {
		if got := forTokenizer(in); got != want {
			t.Errorf("forTokenizer(%q) = %q, want %q", in, got, want)
		}
	}
}
