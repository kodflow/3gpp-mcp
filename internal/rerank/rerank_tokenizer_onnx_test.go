//go:build onnx

package rerank

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sugarme/tokenizer/pretrained"
)

// THE CROSS-ENCODER TOKENIZES EVERY PASSAGE SHAPE THE ENGINE BUILDS. Engine.rerank
// sends heading + "\n" + text, so a clause with an empty body arrives ending in
// "\n" — and the real tokenizer.json panicked on that, inside the stdio worker,
// leaving the client with no answer (found by the served retrieval gate,
// 2026-09-11). Needs the model directory: BGE_RERANKER_DIR, or the repository's
// data/models, which a worktree does not have.
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
	for _, heading := range []string{"Foreword", "5.1 Scope", "0 1 0 1 1 5", "Annex A (informative):\tChange history"} {
		for _, text := range []string{"", "\n", "\r\n", "\t"} {
			passage := heading + "\n" + text // the shape Engine.rerank builds
			if _, err := encodePair(tok, forTokenizer(query), forTokenizer(passage)); err != nil {
				t.Errorf("passage %q: %v", passage, err)
			}
			// A query with a trailing newline, as a client may send it.
			if _, err := encodePair(tok, forTokenizer(query+"\n"), forTokenizer(passage)); err != nil {
				t.Errorf("query with a trailing newline, passage %q: %v", passage, err)
			}
		}
	}
	// encodePair turns the library's panic into an error instead of unwinding into
	// the MCP worker. If the library stops panicking on this shape, say so rather
	// than fail: the trim is then merely redundant.
	if _, err := encodePair(tok, query, "Foreword\n"); err == nil {
		t.Log("the tokenizer no longer panics on a trailing newline — forTokenizer is now redundant")
	} else if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("an untrimmed passage failed, but not as a recovered panic: %v", err)
	}
}
