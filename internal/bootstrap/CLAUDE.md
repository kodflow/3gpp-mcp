<!-- updated: 2026-05-30T08:38:41Z -->
# internal/bootstrap — Self-Provisioning

## Purpose

Fetches the runtime artifacts the server needs but does not ship in the binary:
the indexed DuckDB snapshot and (for the semantic path) the ONNX models +
runtime. Downloads once into a per-user cache, verifies SHA-256, decompresses
transparently — so `mcp-3gpp serve` on a fresh machine is self-provisioning
(CLAUDE.md: mono-binaire, local-first).

## Structure

```text
bootstrap/
├── bootstrap.go   # download + SHA-256 verify + decompress into user cache
├── ghcr_vec.go    # pull vector/runtime artifacts (ghcr.io path)
└── models.go      # model manifest (BGE-M3 / reranker) + pinned versions
```

## Conventions

- The binary **never** pulls a container or runs a daemon — plain artifact
  downloads only (DB from a GitHub Release, models from HuggingFace), read locally.
- Always SHA-256 verify before use; ONNX Runtime version is pinned
  (`ort_pin_test`) — keep the pin and the manifest in sync.
- Migration context: corpus → ghcr, index → Releases, no Git LFS.
