<!-- updated: 2026-05-30T08:38:41Z -->
# internal/onnxrt — Shared ONNX Runtime Init

## Purpose

Centralises ONNX Runtime initialisation. `ort.InitializeEnvironment` is
process-global and errors if called twice, so when both the embedder
(`internal/embed`) and the reranker (`internal/rerank`) are active in the same
process (the serve path embeds the query **and** reranks), they must share one
init. Guarded behind a `sync.Once` + `IsInitialized` check.

## Structure

```text
onnxrt/
└── onnxrt.go   # //go:build onnx — sync.Once-guarded environment init
```

## Conventions

- Entire package is behind the `onnx` build tag — absent from default builds.
- Any new ONNX-backed package must route its init through here, never call
  `ort.InitializeEnvironment` directly.
