# 3gpp-data — the pure-data image (split-data-image plan, PR 1)

## Why

The `:full` mcp image used to re-create its ~22 GB data layer at every build (the
bake produces non-reproducible bytes), so a 3-line code change cost a ~75 min
build, a ~15 GB push and a ~15 GB pull. Splitting **data** from **code** at the
OCI-layer boundary makes the registry's content-addressing do the work: the data
layer is pushed/pulled only when the corpus actually changes.

## The chain (target state, after PR 2)

```text
corpus-matrix (lexical publish)          corpus-embed-kaggle (vectors)
            └────────────┬───────────────────────┘
                         ▼
        corpus-data-image.yml  →  ghcr.io/kodflow/3gpp-data:{latest,YYYY-MM-DD}
        (ONE bake, amd64; multi-platform manifest, SAME data blob)
                         ▼  (workflow_run — wired in PR 2)
        corpus-image.yml       →  ghcr.io/kodflow/3gpp-mcp:{latest,full,light}
        (code-only: ~10 min, ~150 MB — FROM 3gpp-data@digest + COPY --link)
```

## Invariants (enforced fail-loud in CI)

- `3gpp-data` is **copy-only** (`FROM scratch`, no `RUN`): both platform
  manifests must reference the **same data blob** (verify step in the workflow).
- The bake carries every hardening of the #125 chain: identity overlay,
  models-release restore (no HuggingFace on the hot path), DUCK guard,
  embedding_model stamp, compaction on the emptiest disk.
- The package is PRIVATE (verbatim 3GPP text) — set once in the GitHub UI after
  the first push, like 3gpp-corpus / 3gpp-mcp / 3gpp-vec.

## Status

- PR 1 (this doc): additive — `Dockerfile.data` + `corpus-data-image.yml`,
  manual dispatch only; the legacy full bake in `corpus-image.yml` stays
  authoritative.
- PR 2: switch — `Dockerfile` consumes `3gpp-data@digest` (stage `corpus` +
  `COPY --link`), `corpus-image.yml` drops its bake, manifest-based dedupe gate.
- PR 3: cleanup — prune extended to `3gpp-data`, docs completed.

Plan : `.claude/plans/split-data-image.md` (v2, durci par review externe).
