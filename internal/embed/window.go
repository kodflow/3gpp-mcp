package embed

import (
	"math"
	"strings"
)

// defaultWindowWords bounds a window so it stays comfortably under BGE-M3's
// maxTokens (512) for typical prose (~1.3 tokens/word). Used only when the
// EMBED_WINDOWING=mean_pool path is enabled.
const defaultWindowWords = 300

// windowText splits text into ≤maxWords word-windows (no overlap), respecting
// word boundaries (never mid-word). Short text → a single window. Used to embed
// long clauses (tables, ASN.1) in pieces instead of silently truncating at 512.
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
	dim := len(vecs[idx[0]])
	acc := make([]float64, dim)
	for _, k := range idx {
		for j, x := range vecs[k] {
			acc[j] += float64(x)
		}
	}
	out := make([]float32, dim)
	var sum float64
	for j := range acc {
		v := acc[j] / float64(len(idx))
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
