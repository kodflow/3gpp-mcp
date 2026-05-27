// Package embed wraps the BGE-M3 ONNX model used to vectorize clauses.
//
// BGE-M3 produces 1024-dim dense embeddings + a sparse component. V1 only
// uses the dense vector; sparse and ColBERT-style outputs are reserved
// for future work. Runs on CPU (sufficient for ingestion) with optional
// CUDA backend via the ONNX Runtime EP.
//
// The model file lives at data/models/bge-m3.onnx and is NOT committed
// to git. Bootstrap script downloads it on first ingest.
package embed
