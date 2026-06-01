// Package embed vectorises clause text with BGE-M3 (1024-dim dense).
//
// Doctrine (matches corpus.sh / store): embeddings are OPTIONAL. The real
// ONNX Runtime + BGE-M3 backend lives behind the `onnx` build tag and a
// downloaded model (data/models/bge-m3.onnx, not committed). The default build
// returns Disabled{}: ingestion still runs, vectors stay NULL, and retrieval
// degrades to lexical (BM25/LIKE) — visible, never blocking. The target POC
// (counting LI events per NF) is lexical/structured and needs no vectors.
package embed

import (
	"context"
	"os"
	"strings"
)

// Dim is the BGE-M3 dense embedding dimensionality.
const Dim = 1024

// Embedder turns clause texts into dense vectors.
type Embedder interface {
	// Enabled reports whether real vectors are produced.
	Enabled() bool
	// Dim is the vector dimensionality.
	Dim() int
	// Embed returns one Dim-length vector per input text. When the embedder is
	// disabled it returns (nil, nil) and the caller skips vector storage.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// ModelID identifies the model whose vectors this embedder produces, e.g.
	// "bge-m3" or "hash-local" ("" when disabled). It is written to schema_meta
	// at index build and re-checked at serve time: a client embedding queries
	// with model B against a DB indexed with model A yields silently-wrong cosine
	// scores, so the serve guard disables vector search on a mismatch.
	ModelID() string
}

// Disabled is the no-op embedder used when no ONNX model is available.
type Disabled struct{}

func (Disabled) Enabled() bool   { return false }
func (Disabled) Dim() int        { return Dim }
func (Disabled) ModelID() string { return "" }
func (Disabled) Embed(context.Context, []string) ([][]float32, error) {
	return nil, nil
}

// Execution-provider identifiers for the ONNX Runtime session.
const (
	// EPCPU is the default CPU execution provider (no GPU lib needed).
	EPCPU = "cpu"
	// EPCUDA requests the CUDA execution provider. Selecting it COMPILES on any
	// host (the SessionOptions wiring is platform-independent); it only needs a
	// real GPU + the CUDA-enabled ONNX Runtime shared library at RUNTIME. On a
	// host without that, AppendExecutionProviderCUDA fails and the embedder
	// degrades to Disabled{} (degrade, never block — see embed_onnx.go).
	EPCUDA = "cuda"
)

// ExecutionProvider resolves the ONNX Runtime execution provider from the
// ORT_EP env var, normalising to a known value. Anything other than a
// case-insensitive "cuda" (incl. unset, empty, or garbage) maps to CPU, so the
// default stays CPU and a typo can never silently request an unavailable GPU.
// This selection is intentionally tag-free (no `onnx` build tag) so it can be
// unit-tested without the ONNX Runtime / a real GPU.
func ExecutionProvider() string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ORT_EP")), EPCUDA) {
		return EPCUDA
	}
	return EPCPU
}

// New returns the embedder selected by the EMBEDDER env var:
//
//	EMBEDDER=local|hash  -> Local{}     (deterministic vectors; proves the path)
//	EMBEDDER=off|none    -> Disabled{}  (lexical only)
//	(unset)              -> default build: Disabled{}, or ONNX BGE-M3 with -tags onnx
func New() Embedder {
	switch strings.ToLower(os.Getenv("EMBEDDER")) {
	case "local", "hash":
		return Local{}
	case "off", "none", "disabled":
		return Disabled{}
	}
	return newEmbedder()
}
