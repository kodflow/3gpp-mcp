//go:build !onnx

package rerank

// newReranker (default build) has no ONNX runtime, so reranking is disabled.
// Build with `-tags onnx` to compile the bge-reranker-v2-m3 backend instead.
func newReranker() Reranker { return Disabled{} }
