//go:build onnx

package rerank

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sugarme/tokenizer/pretrained"
)

// THE CROSS-ENCODER TOKENIZES EVERY PASSAGE SHAPE THE ENGINE BUILDS, AND FOLDING
// CHANGES NO TOKEN. Engine.rerank sends heading + "\n" + text; the real
// tokenizer.json panicked on a clause with an empty body and on a tabulated line,
// inside the stdio worker, leaving the client with no answer (found by the served
// retrieval gate, 2026-09-11). Needs the model directory: BGE_RERANKER_DIR, or the
// repository's data/models, which a worktree does not have.
func TestTheRerankerTokenizesPassagesWithAnEmptyBody(t *testing.T) {
	dir := envOr("BGE_RERANKER_DIR", filepath.Join("..", "..", "data", "models", "bge-reranker-v2-m3"))
	tok, err := pretrained.FromFile(filepath.Join(dir, "tokenizer.json"))
	if err != nil {
		if _, serr := os.Stat(filepath.Join(dir, "tokenizer.json")); os.IsNotExist(serr) {
			t.Skipf("no reranker tokenizer at %s", dir)
		}
		t.Fatal(err)
	}
	query := "AMF location update interception event"
	// The shapes captured from the corpora: heading-only clauses, an ETSI code
	// row, and the tabulated test-procedure line of ETSI TS 102 695-2 5.5.1.3.4.3.
	for _, passage := range []string{
		"Foreword\n",
		"5.1 Scope\n",
		"0 1 0 1 1 5\n",
		"Foreword\r\n",
		"Test procedure\nStep Direction                                    Description                            RQ",
	} {
		if _, err := encodePair(tok, query, passage); err != nil {
			t.Errorf("passage %q: %v", passage, err)
		}
		if _, err := encodePair(tok, query+"\n", passage); err != nil {
			t.Errorf("query with a trailing newline, passage %q: %v", passage, err)
		}
	}
	// NEUTRAL: on every input the library CAN encode, encodePair returns exactly
	// the tokens the library gives — the fallback is never taken. (Folding those
	// inputs first would NOT be neutral: this library gives "a  b" a stray ▁.)
	for _, passage := range []string{
		"AMF registration\nThe AMF shall report the event over LI_X2.",
		"a\nb", "a  b", "x\ty", "a\n\nb", "  leading", "Scope\n\nbody", "8 8 8 8\nServices   YES",
		"Step Direction    Description",
	} {
		raw, err := tok.EncodePair(query, passage, true)
		if err != nil {
			t.Fatalf("control passage %q does not encode: %v", passage, err)
		}
		served, err := encodePair(tok, query, passage)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(raw.Ids, served.Ids) {
			t.Errorf("encodePair changed the tokens of %q:\n  library %v\n  served  %v", passage, raw.Ids, served.Ids)
		}
	}
	// ONLY THE PASSAGE IS FOLDED when folding it is enough: a query whose own
	// whitespace the library encodes keeps its tokens for a panicking passage.
	spaced := "AMF  location update"
	got, err := encodePair(tok, spaced, "Foreword\n")
	if err != nil {
		t.Fatal(err)
	}
	want, err := tok.EncodePair(spaced, forTokenizer("Foreword\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Ids, want.Ids) {
		t.Errorf("the query was folded along with the passage:\n  got  %v\n  want %v", got.Ids, want.Ids)
	}
	// The library still panics on these shapes: if it stops, the fallback is merely
	// redundant — say so rather than fail.
	func() {
		defer func() { _ = recover() }()
		if _, err := tok.EncodePair(query, "Foreword\n", true); err == nil {
			t.Log("the tokenizer no longer panics on a trailing newline — the fallback may be redundant")
		}
	}()
}
