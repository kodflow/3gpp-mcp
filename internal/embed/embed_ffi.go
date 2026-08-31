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
int embed_core_embed_both(const char* text, float* out_dense, int dense_len, unsigned int* out_ids, float* out_weights, int sparse_cap);
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
//
// The mapping itself lives in ffi_identity.go (untagged, CGO-free) so the contract
// "what this returns == what cmd/embedid stamps" is covered by a plain `go test`.
// It previously returned the bare family name "bge-m3" while the corpus stamps a
// 12-hex EmbedIdentity digest — every valid vectorised DB was therefore served as
// pure lexical, silently. See ffi_identity.go for the full contract.
func (ffiEmbedder) ModelID() string {
	return ffiModelID(C.GoString(C.embed_core_backend()))
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
//
// Same contract as ModelID: the corpus stamps schema_meta.sparse_model with
// `cmd/embedid --sparse` (a digest), so returning the bare "bge-m3-sparse" made
// every sparse-layer comparison fail. Mapping in ffi_identity.go.
func (ffiEmbedder) SparseModelID() string {
	return ffiSparseModelID(C.GoString(C.embed_core_backend()), C.embed_core_has_sparse() == 1)
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

// EmbedBoth returns the dense vector AND the sparse postings for one text from a
// SINGLE forward pass of the model.
//
// A hybrid query used to run the transformer twice over the same string — once
// for each head — and ONNX Runtime computes the shared encoder either way, so the
// second pass bought nothing. Measured on the development machine: ~166 ms for
// the pair, against a BM25 arm that costs ~10 ms, which makes the redundant pass
// roughly half the latency of every non-lexical search.
//
// ok is false when there is no combined path — a dense-only model, or a text long
// enough that the dense and sparse windows would differ — and the caller then uses
// Embed and EmbedSparse as before. That is a fallback, NOT an error: the answer is
// identical either way, only slower.
func (e ffiEmbedder) EmbedBoth(ctx context.Context, text string) (dense []float32, sparse model.SparseVec, ok bool) {
	const sparseCap = 16384 // a query's distinct terms are far below this
	d := make([]float32, int(C.embed_core_dim()))
	ids := make([]uint32, sparseCap)
	weights := make([]float32, sparseCap)

	ct := C.CString(text)
	n := int(C.embed_core_embed_both(ct,
		(*C.float)(unsafe.Pointer(&d[0])), C.int(len(d)),
		(*C.uint)(unsafe.Pointer(&ids[0])), (*C.float)(unsafe.Pointer(&weights[0])), C.int(sparseCap)))
	C.free(unsafe.Pointer(ct))
	if n < 0 {
		return nil, nil, false
	}
	w := n
	if w > sparseCap {
		w = sparseCap
	}
	sv := make(model.SparseVec, w)
	for j := 0; j < w; j++ {
		sv[ids[j]] = weights[j]
	}
	return d, sv, true
}
