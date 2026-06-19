//go:build !onnx && !embed_ffi

package embed

// newEmbedder (default build) has no ONNX runtime, so vectors are disabled.
// Build with `-tags onnx` to compile the BGE-M3 backend instead.
func newEmbedder() Embedder { return Disabled{} }
