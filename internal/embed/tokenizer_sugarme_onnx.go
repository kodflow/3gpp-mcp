//go:build onnx && !fasttok

package embed

import (
	"errors"

	"github.com/sugarme/tokenizer"
	"github.com/sugarme/tokenizer/pretrained"
)

// sugarmeTokenizer is the pure-Go (no native dependency) clauseTokenizer — the
// default onnx build. Correct but slow (~17 clause/s/thread locally, ~1.5 on
// Kaggle's vCPUs), so it is the GPU-starving path the fasttok build replaces.
type sugarmeTokenizer struct{ t *tokenizer.Tokenizer }

func (s sugarmeTokenizer) EncodeIDs(text string) ([]int, error) {
	enc, err := s.t.EncodeSingle(text, true)
	if err != nil {
		return nil, err
	}
	return enc.Ids, nil
}

// newClauseTokenizers builds a pool of n pure-Go tokenisers from tokenizer.json.
// At least one must load (else the embedder disables); a later failure degrades to
// fewer producers rather than blocking.
func newClauseTokenizers(path string, n int) ([]clauseTokenizer, error) {
	out := make([]clauseTokenizer, 0, n)
	for i := 0; i < n; i++ {
		t, err := pretrained.FromFile(path)
		if err != nil {
			if i == 0 {
				return nil, err
			}
			break
		}
		out = append(out, sugarmeTokenizer{t})
	}
	if len(out) == 0 {
		return nil, errors.New("embed: no tokenizer loaded")
	}
	return out, nil
}

// tokenizerImpl names the active implementation for the readiness log line.
func tokenizerImpl() string { return "sugarme-purego" }
