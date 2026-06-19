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
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"
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
