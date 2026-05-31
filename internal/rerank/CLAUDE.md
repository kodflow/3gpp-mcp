<!-- updated: 2026-05-30T08:38:41Z -->
# internal/rerank — Cross-Encoder Reranker Seam

## Purpose

Optional cross-encoder reranker (axis #7). After RRF fusion the engine retrieves
broad (top-20); the reranker re-scores `(query, passage)` pairs to return a
sharper top-k. Mirrors the `embed` seam.

## Structure

```text
rerank/
├── rerank.go        # Reranker interface + selection (RERANKER env)
├── rerank_noop.go   # default Disabled{} (degrade, never block)
└── rerank_onnx.go   # bge-reranker-v2-m3 ONNX backend (build tag `onnx`)
```

## Conventions

- Default build is `Disabled{}` — **degrade, never block** on a missing model.
- Real backend behind `-tags onnx`; a dependency-free Lexical reranker proves the
  path without a model download.
- Shares ONNX Runtime init with `embed` via `internal/onnxrt` (serve path embeds
  the query AND reranks in the same process → must init once).
