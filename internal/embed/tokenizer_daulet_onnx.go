//go:build onnx && fasttok

package embed

import (
	"errors"

	"github.com/daulet/tokenizers"
)

// dauletTokenizer is the HuggingFace Rust clauseTokenizer via CGO (the `fasttok`
// build). It is the reference tokenisation (the same library sentence-transformers
// uses) and orders of magnitude faster than the pure-Go path, so it keeps the GPU
// fed instead of starving it (the 2×T4 probe measured GPU util at 3-5% on the
// sugarme path). Requires libtokenizers.a at link time (CGO_LDFLAGS=-L<dir>).
type dauletTokenizer struct{ t *tokenizers.Tokenizer }

func (d dauletTokenizer) EncodeIDs(text string) ([]int, error) {
	ids, _ := d.t.Encode(text, true) // []uint32, WITH special tokens
	out := make([]int, len(ids))
	for i, id := range ids {
		out[i] = int(id)
	}
	return out, nil
}

// newClauseTokenizers builds a pool of n HF Rust tokenisers from tokenizer.json.
// HF encode is thread-safe, but we keep a pool to mirror the sugarme path and avoid
// any shared-state assumption. The native tokeniser is freed at process exit (this
// is a batch tool); we deliberately do not Close per-call.
func newClauseTokenizers(path string, n int) ([]clauseTokenizer, error) {
	out := make([]clauseTokenizer, 0, n)
	for i := 0; i < n; i++ {
		t, err := tokenizers.FromFile(path)
		if err != nil {
			if i == 0 {
				return nil, err
			}
			break
		}
		out = append(out, dauletTokenizer{t})
	}
	if len(out) == 0 {
		return nil, errors.New("embed: no tokenizer loaded")
	}
	return out, nil
}

// tokenizerImpl names the active implementation for the readiness log line.
func tokenizerImpl() string { return "daulet-hf-rust" }
