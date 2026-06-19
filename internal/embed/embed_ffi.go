//go:build embed_ffi

// embed_ffi wires the Go serve query-embed to the SHARED Rust embedder (rust/embed-core) over
// its cdylib C ABI — Phase 1 of the write-side→Rust plan: the same Rust inference path the
// corpus pipeline uses, instead of the Go ONNX backend, resolving the internal/embed
// write/read coupling. Selected with `-tags embed_ffi`; the cdylib (libembed_core.so) must be
// built (cargo build --release --manifest-path rust/embed-core/Cargo.toml) and findable at
// run time (the rpath below points at the release dir). The baseline cdylib is a
// deterministic hash embedder (ModelID "hash-local"); the real BGE-M3 ONNX inference is the
// same crate built with `--features ort` (ModelID "bge-m3-onnx"), validated in CI/Kaggle.
package embed

/*
#cgo LDFLAGS: -L${SRCDIR}/../../rust/embed-core/target/release -Wl,-rpath,${SRCDIR}/../../rust/embed-core/target/release -lembed_core
#include <stdlib.h>
int embed_core_dim(void);
int embed_core_embed(const char* text, float* out, int out_len);
const char* embed_core_backend(void);
int embed_core_has_sparse(void);
int embed_core_embed_sparse(const char* text, unsigned int* out_ids, float* out_weights, int cap);
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"

	"github.com/kodflow/3gpp-mcp/internal/model"
)

// ffiEmbedder calls rust/embed-core over the C ABI. It satisfies embed.Embedder so the serve
// query path is backend-agnostic.
type ffiEmbedder struct{}

// newEmbedder (embed_ffi build) returns the cdylib-backed embedder.
func newEmbedder() Embedder { return ffiEmbedder{} }

func (ffiEmbedder) Enabled() bool { return true }

func (ffiEmbedder) Dim() int { return int(C.embed_core_dim()) }

// ModelID mirrors the cdylib's backend so the serve coherence guard compares the query
// embedder against the corpus embedding_model (a mismatch disables vector search).
func (ffiEmbedder) ModelID() string {
	switch C.GoString(C.embed_core_backend()) {
	case "hash":
		return "hash-local" // matches a Local-embedded corpus (seam validation)
	default:
		return "bge-m3" // real ONNX path (ort feature); family id the corpus uses
	}
}

// Embed returns one Dim-length vector per text via the cdylib. cgo copies each query in/out
// across the boundary; the C side fills a caller-owned buffer (no Rust allocation escapes).
func (ffiEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	dim := int(C.embed_core_dim())
	if dim <= 0 {
		return nil, fmt.Errorf("embed_core_dim returned %d", dim)
	}
	out := make([][]float32, len(texts))
	buf := make([]float32, dim)
	for i, t := range texts {
		ct := C.CString(t)
		rc := C.embed_core_embed(ct, (*C.float)(unsafe.Pointer(&buf[0])), C.int(dim))
		C.free(unsafe.Pointer(ct))
		if rc != 0 {
			return nil, fmt.Errorf("embed_core_embed(%q) rc=%d", t, rc)
		}
		v := make([]float32, dim)
		copy(v, buf)
		out[i] = v
	}
	return out, nil
}

// SparseModelID identifies the FFI sparse arm; "" when the loaded model has no sparse head
// (dense-only / hash baseline) so the engine simply does not offer the sparse arm.
func (ffiEmbedder) SparseModelID() string {
	if C.embed_core_has_sparse() == 1 {
		return "bge-m3-sparse"
	}
	return ""
}

// EmbedSparse returns one post-processed sparse vector per text via the cdylib's sparse head
// (term_id → max ReLU weight, specials + non-positive dropped — identical to the corpus
// EmbedSparse). Errors (engine degrades to dense+BM25) when the model has no sparse head.
func (ffiEmbedder) EmbedSparse(_ context.Context, texts []string) ([]model.SparseVec, error) {
	if C.embed_core_has_sparse() != 1 {
		return nil, fmt.Errorf("embed-core: model has no sparse head")
	}
	const cap = 16384 // a query's distinct sparse terms are ≪ this; grow only if rc > cap
	out := make([]model.SparseVec, len(texts))
	ids := make([]uint32, cap)
	weights := make([]float32, cap)
	for i, t := range texts {
		ct := C.CString(t)
		n := int(C.embed_core_embed_sparse(ct,
			(*C.uint)(unsafe.Pointer(&ids[0])), (*C.float)(unsafe.Pointer(&weights[0])), C.int(cap)))
		C.free(unsafe.Pointer(ct))
		if n < 0 {
			return nil, fmt.Errorf("embed_core_embed_sparse(%q) rc=%d", t, n)
		}
		w := n
		if w > cap {
			w = cap // truncated to the buffer; queries never hit this in practice
		}
		sv := make(model.SparseVec, w)
		for j := 0; j < w; j++ {
			sv[ids[j]] = weights[j]
		}
		out[i] = sv
	}
	return out, nil
}
