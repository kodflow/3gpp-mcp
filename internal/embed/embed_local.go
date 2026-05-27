package embed

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// Local is a dependency-free, deterministic embedder: a signed feature-hashing
// (hashing-trick) bag-of-words projected into Dim and L2-normalised. It is NOT
// a semantic model — it exists to prove the vector path end-to-end (populate
// clauses.embedding, build the HNSW index, run cosine search) without the
// ~2 GB BGE-M3 download or a SentencePiece tokenizer. The production semantic
// backend is the ONNX BGE-M3 behind `-tags onnx` (see embed_onnx.go).
//
// Enable with EMBEDDER=local (or hash). Cosine similarity here reflects token
// overlap — enough to demonstrate ranking and HNSW recall.
type Local struct{}

func (Local) Enabled() bool   { return true }
func (Local) Dim() int        { return Dim }
func (Local) ModelID() string { return "hash-local" }

func (Local) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = hashVec(t)
	}
	return out, nil
}

func hashVec(text string) []float32 {
	v := make([]float32, Dim)
	for _, tok := range tokenize(text) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(tok))
		s := h.Sum32()
		idx := s % uint32(Dim)
		sign := float32(1)
		if s&1 == 1 { // independent sign bit reduces collisions
			sign = -1
		}
		v[idx] += sign
	}
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	if n > 0 {
		inv := float32(1 / math.Sqrt(n))
		for j := range v {
			v[j] *= inv
		}
	}
	return v
}

func tokenize(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := fields[:0]
	for _, f := range fields {
		if len(f) >= 2 {
			out = append(out, f)
		}
	}
	return out
}
