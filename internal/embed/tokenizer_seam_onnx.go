//go:build onnx

package embed

// clauseTokenizer is the embed tokeniser seam: it turns one clause into token IDs
// WITH special tokens (CLS … SEP), exactly as the model's tokenizer.json defines.
//
// Two implementations are swapped by build tag, because the Kaggle 2×T4 probe showed
// the embed run is CPU-tokenise bound (GPU idle ~96%, util_mean 3-5%): the pure-Go
// sugarme/tokenizer (default onnx build, no native dependency) tokenises at only
// ~1.5 clause/s/thread on Kaggle's vCPUs and starves the GPU, while the HuggingFace
// Rust tokenizers via CGO (-tags "onnx fasttok") are orders of magnitude faster and
// keep the GPU fed. We hold a POOL (one instance per producer goroutine) rather than
// share one, so we never depend on either implementation being concurrency-safe.
//
// Both load the SAME tokenizer.json, but sugarme is a re-implementation and the HF
// Rust library is the reference — their IDs can differ on edge cases, so a corpus
// must be embedded entirely with ONE implementation (the GPU rebuild is all-fasttok;
// the published `latest` DB is lexical, carrying no vectors, so they never mix).
type clauseTokenizer interface {
	// EncodeIDs tokenises text into model input IDs (with special tokens). It mirrors
	// sugarme's EncodeSingle(text, true) so the two implementations are drop-in.
	EncodeIDs(text string) ([]int, error)
}
