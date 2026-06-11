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

- PR 1 (#138, merged): additive — `Dockerfile.data` + `corpus-data-image.yml`
  (manual dispatch; chain wiring landed with PR 2).
- PR 2 (#139): switch — multi-target `Dockerfile` (base/light/full) consuming
  `3gpp-data@digest` (stage `corpus` + `COPY --link`), `corpus-image.yml`
  bake removed, `resolve` job (digest + ORT pins) + manifest-based `gate` job.
- PR 3 (this change): cleanup — prune extended to `3gpp-data` (keep `:latest`
  + 7 dated, each version ~15 GB compressed), `make inspect-layers` for local
  dedupe eyeballing, docs completed.

## Local helpers

- `make image-light` — local light build (now `--target light`).
- `make inspect-layers` — per-platform layers of the published `:latest` with
  the `3gpp-data` blob highlighted (requires `crane` + GHCR login).

Plan : `.claude/plans/split-data-image.md` (v2, durci par review externe).
