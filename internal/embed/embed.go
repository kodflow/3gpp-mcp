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
}

// Disabled is the no-op embedder used when no ONNX model is available.
type Disabled struct{}

func (Disabled) Enabled() bool { return false }
func (Disabled) Dim() int      { return Dim }
func (Disabled) Embed(context.Context, []string) ([][]float32, error) {
	return nil, nil
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
