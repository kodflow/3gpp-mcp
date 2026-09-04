package embed

// sparse.go — the (optional) sparse seam, tag-free so the interface, the
// deterministic Local backend, and the pure post-processing are unit-testable
// without ONNX. The real BGE-M3 sparse output is read by the onnx backend behind
// `-tags onnx` (embed_onnx.go), which reuses toSparse here.

import (
	"context"
	"hash/fnv"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// SparseEmbedder is implemented by embedders that ALSO produce BGE-M3 learned
// lexical (sparse) weights. The engine type-asserts an Embedder to this interface;
// when absent (Disabled, or an onnx model without the sparse head), the sparse arm
// is simply not offered (degrade, never block). The sparse vectors share the dense
// embedder's ModelID (one model produces both).
type SparseEmbedder interface {
	// EmbedSparse returns one post-processed sparse vector per input text
	// (token_id → weight, specials + non-positive dropped, max per id).
	EmbedSparse(ctx context.Context, texts []string) ([]model.SparseVec, error)
	// SparseModelID is the identity of the sparse arm this embedder produces. It is
	// stamped into DB meta (sparse_model) when `--sparse-only` completes so the bake
	// and serve can tell which sparse layer (if any) the DB carries.
	SparseModelID() string
}

// xlmrSpecialTokens are the BGE-M3 (XLM-RoBERTa) special token ids dropped from
// the sparse representation: <s>/cls=0, <pad>=1, </s>/eos=2, <unk>=3. The real
// onnx backend should prefer resolving these from the tokenizer, but these are the
// fixed XLM-R ids and a safe default backstop (research brief §d).
var xlmrSpecialTokens = map[uint32]bool{0: true, 1: true, 2: true, 3: true}

// toSparse reproduces FlagEmbedding's _process_token_weights: from per-position
// token ids + raw ReLU weights, keep the MAX weight per token id, drop special
// tokens and non-positive weights. ids and weights must be the same length (the
// sequence the model saw); extra weights are ignored.
func toSparse(ids []uint32, weights []float32, special map[uint32]bool) model.SparseVec {
	if special == nil {
		special = xlmrSpecialTokens
	}
	out := make(model.SparseVec, len(ids))
	n := len(ids)
	if len(weights) < n {
		n = len(weights)
	}
	for i := 0; i < n; i++ {
		id, w := ids[i], weights[i]
		if w <= 0 || special[id] {
			continue
		}
		if cur, ok := out[id]; !ok || w > cur {
			out[id] = w
		}
	}
	return out
}

// EmbedSparse makes Local a SparseEmbedder: a deterministic stand-in that hashes
// each token to a pseudo vocab id and weights it by sqrt(term frequency). NOT
// semantic — it exists to exercise the sparse arm end-to-end (store, fuse, serve)
// under EMBEDDER=local, exactly as hashVec does for the dense path.
func (Local) EmbedSparse(_ context.Context, texts []string) ([]model.SparseVec, error) {
	out := make([]model.SparseVec, len(texts))
	for i, t := range texts {
		counts := map[uint32]float32{}
		for _, tok := range tokenize(t) {
			h := fnv.New32a()
			_, _ = h.Write([]byte(tok))
			// +16 keeps ids clear of the reserved special-token range (0..3).
			id := h.Sum32()%250000 + 16
			counts[id]++
		}
		vec := make(model.SparseVec, len(counts))
		for id, c := range counts {
			// sqrt(tf) — a stable, monotonic weight in a BGE-M3-like 0..~0.5 range.
			vec[id] = float32(0.4) * sqrt32(c) / (1 + sqrt32(c))
		}
		out[i] = vec
	}
	return out, nil
}

// SparseModelID identifies Local's deterministic sparse arm (distinct from its
// dense "hash-local"), so a DB populated under EMBEDDER=local carries a meaningful,
// stable sparse_model marker in tests.
func (Local) SparseModelID() string { return "hash-local-sparse" }

func sqrt32(x float32) float32 {
	if x <= 0 {
		return 0
	}
	// Newton's method, a few iterations — avoids importing math for one call site
	// in the hot tokenise loop and keeps the value deterministic across platforms.
	z := x
	for i := 0; i < 8; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// DualEmbedder produces BOTH heads of a dual-head model from ONE forward pass.
//
// BGE-M3 emits the dense sentence embedding and the learned-lexical weights from
// the same encoder, and ONNX Runtime computes that encoder whether one output is
// read or two. A hybrid query that called Embed and then EmbedSparse therefore ran
// the transformer twice over the same string for nothing — measured at ~166 ms for
// the pair against a BM25 arm costing ~10 ms, so the redundant pass was about half
// the latency of every non-lexical search.
//
// ok is false when there is no combined path (a dense-only model, or a text long
// enough that the dense and sparse windows would diverge). That is a FALLBACK, not
// an error: the caller uses Embed and EmbedSparse and gets an identical answer,
// only slower. Equivalence is asserted bit-for-bit by
// internal/embed's EmbedBoth test, because a combined path that returns
// almost-the-same vectors would be a silent quality regression rather than a
// speed-up.
type DualEmbedder interface {
	EmbedBoth(ctx context.Context, text string) (dense []float32, sparse model.SparseVec, ok bool)
}
