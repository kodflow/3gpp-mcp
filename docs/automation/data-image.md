# 3gpp-data — the corpus-data base image (split-data-image plan)

## Why

The `:full` mcp image used to re-create its ~14 GB data layer at every build (the
bake produces non-reproducible bytes), so a 3-line code change cost a ~75 min
build, a ~15 GB push and a ~15 GB pull. Splitting **data** from **code** at the
OCI-layer boundary makes the registry's content-addressing do the work: the data
layer is pushed/pulled only when the corpus actually changes.

## How — `FROM` inheritance (NOT `COPY`)

The first cut used `COPY --link --from=3gpp-data`. The gate caught that this does
**not** share the source blob: `COPY` (even `--link`) re-tars the files into a
fresh content-addressed layer (a different 14 GB blob per arch). Only **`FROM`
inheritance** lists the base's layers verbatim in the child manifest. So:

- `3gpp-data` is **debian-based** (a `FROM scratch` image cannot be a usable
  base), built **multi-arch** — each platform carries its **own** data layer.
- `full = FROM ${DATA_IMAGE}`: full's manifest **references** that arch's exact
  3gpp-data data blob by digest. A code-only rebuild re-creates only the small
  top layers (apt libs, user, binary, ORT); the data layer is inherited and the
  registry/pull dedupe it.

Trade-off (user-approved): the data image's registry footprint doubles (one
~14 GB blob per arch) vs the old scratch single-blob — the price of guaranteed
per-arch sharing.

## The chain

```text
corpus-matrix (lexical publish)          corpus-embed-kaggle (vectors)
            └────────────┬───────────────────────┘
                         ▼
        corpus-data-image.yml  →  ghcr.io/kodflow/3gpp-data:{latest,YYYY-MM-DD}
        (ONE bake, amd64; debian multi-arch — per-arch data layer)
                         ▼  (workflow_run)
        corpus-image.yml       →  ghcr.io/kodflow/3gpp-mcp:{latest,full,light}
        (code-only: ~10 min, ~150 MB — full = FROM 3gpp-data@digest)
```

## Invariants (enforced fail-loud in CI)

- `3gpp-data` is **multi-arch** (debian base, RUN-free → no QEMU): the bake's
  verify step asserts both `linux/amd64` and `linux/arm64` exist and each carries
  a >1 GiB data layer.
- `corpus-image.yml`'s `gate` asserts each mcp platform's manifest references
  **that arch's** exact 3gpp-data data blob (FROM-inheritance proof) and that it
  is the only >1 GiB layer.
- The bake carries every hardening of the #125 chain: identity overlay,
  models-release restore (no HuggingFace on the hot path), DUCK guard,
  embedding_model stamp, compaction on the emptiest disk.
- The package is PRIVATE (verbatim 3GPP text) — born private via the PAT push;
  every publish workflow self-heals + asserts it, like 3gpp-corpus / 3gpp-mcp /
  3gpp-vec.

## Pulling from the labs

The labs pulls the single private `3gpp-mcp` image with a **dedicated read-only
token** — see [labs-pull.md](labs-pull.md) for the token, the Docker/Kubernetes
recipes, and both rotation procedures (image + token).

## Local helpers

- `make image-light` — local light build (now `--target light`).
- `make inspect-layers` — per-platform layers of the published `:latest` with
  the `3gpp-data` blob highlighted (requires `crane` + GHCR login).

Plan : `.claude/plans/split-data-image.md` (v2, durci par review externe).
