<!-- updated: 2026-05-30T08:38:41Z -->
# internal/embed — BGE-M3 Embedder Seam

## Purpose

Wraps the BGE-M3 ONNX model used to vectorize clauses. Produces 1024-dim dense
embeddings (sparse / ColBERT outputs reserved for future work). CPU is
sufficient for ingestion; optional CUDA via the ONNX Runtime EP.

## Structure

```text
embed/
├── embed.go        # Embedder interface + selection (EMBEDDER env)
├── embed_local.go  # dependency-free local/lexical embedder (no model download)
├── embed_noop.go   # default no-op (degrade, never block)
├── embed_onnx.go   # real BGE-M3 backend (build tag `onnx`)
├── embed_safe.go   # panic/error guards around the backend
├── window.go       # 8k-context windowing / chunk packing
└── doc.go          # model contract
```

## Conventions

- **Degrade, never block**: default build is no-op; the real backend is behind
  `-tags onnx`. Mirrors the `rerank` seam.
- Model file `data/models/bge-m3.onnx` is **not** committed; `bootstrap` fetches
  it from HuggingFace on first ingest.
- Batch size 32, dimensions 1024 — part of the reproducibility contract (§6).
- Share ONNX Runtime init via `internal/onnxrt` (process-global, must init once).
- `embed_onnx_safe_test` / `window_test` pin guard + windowing behaviour.
