package embed

import (
	"math"
	"strings"
)

// defaultWindowWords bounds a window so it stays comfortably under BGE-M3's
// maxTokens (512) for typical prose (~1.3 tokens/word). Used only when the
// EMBED_WINDOWING=mean_pool path is enabled.
const defaultWindowWords = 300

// This is the REFERENCE implementation of the mean_pool WORD SPLIT. The production
// embedder is Rust (rust/embedder/src/window.rs), which mirrors these two functions
// exactly; the pair is pinned by testdata/window_parity.json (window_parity_test.go),
// because windowing is an EmbedIdentity component and a silent divergence would
// embed the corpus under one split while queries used another.
//
// The Rust side WRAPS this with two things Go does not need, because Go no longer
// embeds the corpus — it only embeds queries, which are short:
//
//   - it windows a clause only when the clause does not fit in max_tokens whole, so
//     a clause that was never truncated keeps the vector it already had;
//   - it re-splits any window that still reaches max_tokens, because 3GPP tables and
//     ASN.1 tokenise at over 3 tokens/word and 300-word windows hit the cap 10.8% of
//     the time on this corpus.
//
// windowText splits text into ≤maxWords word-windows (no overlap), respecting
// word boundaries (never mid-word). Short text → a single window. Used to embed
// long clauses (tables, ASN.1) in pieces instead of silently truncating.
func windowText(text string, maxWords int) []string {
	if maxWords < 1 {
		maxWords = defaultWindowWords
	}
	words := strings.Fields(text)
	if len(words) <= maxWords {
		return []string{text}
	}
	out := make([]string, 0, (len(words)+maxWords-1)/maxWords)
	for i := 0; i < len(words); i += maxWords {
		end := i + maxWords
		if end > len(words) {
			end = len(words)
		}
		out = append(out, strings.Join(words[i:end], " "))
	}
	return out
}

// meanPoolL2 averages the vectors at idx (component-wise) and L2-normalises the
// result — the pooled embedding of a multi-window clause. One window → that
// vector unchanged (already unit norm). Empty idx → nil.
func meanPoolL2(vecs [][]float32, idx []int) []float32 {
	switch len(idx) {
	case 0:
		return nil
	case 1:
		return vecs[idx[0]]
	}
	// Pool only the non-nil windows: a window whose clause could not be tokenised
	// is left UNembedded (nil) rather than corrupted (see tokenizeSafe), so it must
	// not drag the mean toward zero. dim comes from the first real window.
	var dim, count int
	for _, k := range idx {
		if vecs[k] != nil {
			dim = len(vecs[k])
			count++
		}
	}
	if count == 0 {
		return nil
	}
	acc := make([]float64, dim)
	for _, k := range idx {
		for j, x := range vecs[k] {
			acc[j] += float64(x)
		}
	}
	out := make([]float32, dim)
	var sum float64
	for j := range acc {
		v := acc[j] / float64(count)
		out[j] = float32(v)
		sum += v * v
	}
	if sum > 0 {
		inv := float32(1.0 / math.Sqrt(sum))
		for j := range out {
			out[j] *= inv
		}
	}
	return out
}
