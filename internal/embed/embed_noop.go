//go:build !embed_ffi

package embed

// newEmbedder (any build WITHOUT embed_ffi) returns the disabled embedder: query
// vectors are off. The Go ONNX embed backend was removed in the write-side→Rust
// cutover (Phase 11b) — real query-embed is now the embed-core cdylib, selected
// with `-tags embed_ffi` (the shipped image builds onnx,embed_ffi: reranker on
// onnx, embed on the cdylib). `-tags onnx` alone now means "reranker, no embed".
func newEmbedder() Embedder { return Disabled{} }
