# Data Pipeline & Distribution

> How 3GPP sources become the indexed DB ("R"), and how the `mcp-3gpp` binary
> gets it — without ever putting the corpus in git, and without accumulating
> versions.

> [!IMPORTANT]
> **The corpus does NOT travel on the GitHub Release.** This document used to
> describe it doing exactly that, which put the documented design in direct
> contradiction with [`DATA_NOTICE.md`](../DATA_NOTICE.md): the DB holds verbatim
> 3GPP/ETSI clause text, and no release asset of this public repository may carry
> it. It also stopped fitting — GitHub caps an asset at 2 GB, and the
> content-addressed corpus is 21.2 GB (measured 2026-09-01, after the sparse
> layer and the HNSW index).
>
> The sections below describe the flow that is actually in use. The reasoning
> about volumes, deltas and clobbering carried over from the retired one; the
> instructions did not, because a warning at the top does not make an
> executable `gh release upload` of the corpus safe to follow.

## The model: binaries roll on `latest`, the corpus lives in a private package

There is **exactly one GitHub Release, tagged `latest`**. It holds the current
binaries and small **metadata** only. Assets are replaced in place (clobbered);
the release is never deleted, old artifacts never accumulate — no version
history, no disk-space creep.

The corpus is pushed by `scripts/local/publish-corpus.sh` to
`ghcr.io/<owner>/3gpp-corpus`, a **private** package, as one layer holding
`/3gpp.duckdb`.

```text
GitHub Release `latest` — PUBLIC, carries no clause text
└── corpus-index.json                            ← the delta anchor:
                                                    spec|release → highest indexed
                                                    version. A version list, no text.
    (the binary archives it used to carry came from release.yml, which is deleted;
     build one with `make build-bin`, or pull the image, which needs no install)

ghcr.io/<owner>/3gpp-mcp:latest    — PRIVATE, THE PRODUCT: server + both corpora
                                     + models. Built by `make image` on the machine
                                     that holds the corpus.
ghcr.io/<owner>/3gpp-corpus:latest — PRIVATE, one layer = /3gpp.duckdb
ghcr.io/<owner>/etsi-corpus:latest — PRIVATE, one layer = /etsi.duckdb
                                     (the corpus packages remain for the binary's
                                      bootstrap path; the image needs neither)

Client:  docker run -i --rm ghcr.io/<owner>/3gpp-mcp:latest   ← nothing to fetch
    or:  mcp-3gpp bootstrap → GHCR manifest → layer (Range-resumable,
                              digest-verified) → /3gpp.duckdb → serve (offline)
                              needs read:packages; see docs/install.md
```

**Why no versions:** a single frozen `latest`, old ones deleted, to avoid disk
abuse. The DB itself records what specs/versions are indexed, so the DB *is* the
state — no manifest history needed. The same rule holds on the GHCR side: a dated
tag plus a rolling `:latest`.

## Volumes & where things live

| Asset | Size | Where | In git? |
|---|---|---|---|
| Go source | small | git | ✅ |
| 3GPP DOCX/.doc sources | ~20 GB | transient on the sync runner | ❌ |
| Converted HTML | derived | transient on the sync runner | ❌ |
| Indexed DB `3gpp.duckdb` | 21.2 GB | `ghcr.io/<owner>/3gpp-mcp`, private | ❌ |
| Indexed DB `etsi.duckdb` | 8.0 GB | same image, same layer | ❌ |
| BGE-M3 (dense+sparse) / reranker / ONNX RT | 6.4 GB | baked into the image | ❌ |

The corpus never enters git or a Release as raw files. Models stay on
HuggingFace (`model.onnx_data` is 2.2 GB — over the 2 GB Release-asset cap
anyway). The corpus does not fit either — 29.2 GB in one layer against a 2 GB
cap — which is the second, independent reason it travels as an OCI layer.

## Workflows — there is one left

| Workflow | Trigger | Does |
|---|---|---|
| `post-commit.yml` | PR | Gate commit trailers and authorship. |

`ci.yml`, `release.yml`, `rust-bins.yml`, `corpus-image.yml` and
`corpus-data-image.yml` are **deleted**. The image ones moved ~14 GB per run,
which is the resource this project does not have; the rest went with them when
the build moved onto the machine that already holds the corpus.

Everything they did happens locally now, and the corpus never had a CI path
anyway — the constraint below (a hosted runner has ~14 GB disk against a ~20 GB
corpus) was always the reason. `make build` runs the pipeline, `make image`
cross-compiles the Linux artefacts and pushes
`ghcr.io/kodflow/3gpp-mcp:latest` with crane; `post-commit.yml` stays because it
is the status the branch ruleset requires, and deleting it would block every
merge. See [automation/data-image.md](automation/data-image.md).

### Corpus sync — incremental, DB-as-state

The hard constraint: a hosted runner has ~14 GB disk / 7 GB RAM, so the ~20 GB
corpus is **never** fully re-ingested in CI. Each run is a delta:

1. Pull the current corpus from `ghcr.io/<owner>/3gpp-corpus:latest` — one
   manifest request when the cache already holds it, a Range-resumable,
   digest-verified layer pull when it does not.
2. Diff the live 3gpp.org listing against what the DB already contains.
3. Download **only the missing specs** → convert (LibreOffice) →
   `rust/ingest --resume`, whose ledger can be pointed at the published corpus
   so it asks what is ALREADY HELD rather than what this shard remembers.
4. Push the refreshed corpus back to the package with
   `scripts/local/publish-corpus.sh`, which re-asserts that the package is
   private before it uploads.

Kept deliberately simple: "download what's missing, append, re-publish." No
dated releases, no elaborate delta engine.

**The incremental path is not the blocker — it exists.** `rust/ingest --resume`
is ledger-backed and corpus-aware, and `goal run` drives the whole 20-step
pipeline (discover → fetch → ingest → merge → embed → enrich → index →
validate → smoke, plus the ETSI arm). What is missing is only the scheduled
workflow that runs it unattended, and the reason is the runner: the embed step
wants a GPU and a hosted runner has ~14 GB disk / 7 GB RAM. A full rebuild runs
locally on a machine with a GPU — never on hosted CI.

## Client experience

```bash
curl -fsSL https://raw.githubusercontent.com/kodflow/3gpp-mcp/main/scripts/install.sh | sh
export GHCR_PAT=<token with read:packages>
mcp-3gpp bootstrap     # pulls the corpus from the private package, digest-verified
mcp-3gpp serve         # MCP over stdio, offline from here
```

`install.sh` resolves `releases/latest/download/…` for the **binary** — a stable
URL that always points at the one current build. The corpus does not travel that
way and needs a credential; `docs/install.md` explains how to get one.

### Auto-update: the manifest digest is the version signal

The DB has no version number — the **digests of its layer are its identity**.
`bootstrap` records them beside the cached file as `3gpp.duckdb.digest`. At
`serve` **startup** (never at query time — local-first):

1. One authenticated manifest GET returns the published layer digests.
2. Compare to the sidecar next to the cached DB.
   - differs / no cache → pull the layer, verify it against the digest, atomic
     swap;
   - same → use cache, **no download** (the corpus layer moves only when it changed);
   - offline / fetch error / credential gone → keep the cached DB
     (degrade-don't-block).
3. Opt-out via `--no-update` / `MCP3GPP_NO_UPDATE=1` (air-gapped).

The same comparison guards `bootstrap` itself and the pipeline's `seed` step:
it lives in `bootstrap.FetchCorpus`, so no caller has to remember it.

## Open items

- Implement `ingest --append` + wire the corpus-sync workflow.
- [#208](https://github.com/kodflow/3gpp-mcp/issues/208): mean_pool windowing in
  the Rust embedder. It changes `EmbedIdentity`, so it re-embeds the whole
  corpus — a decision to take before a campaign, never during one.
