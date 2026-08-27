# Data Pipeline & Distribution

> How 3GPP sources become the indexed DB ("R"), and how the `mcp-3gpp` binary
> gets it — without ever putting the corpus in git, and without accumulating
> versions.

> [!IMPORTANT]
> **The corpus does NOT travel on the GitHub Release.** This document used to
> describe it doing exactly that, which put the documented design in direct
> contradiction with [`DATA_NOTICE.md`](../DATA_NOTICE.md): the DB holds verbatim
> 3GPP/ETSI clause text, and no release asset of this public repository may carry
> it. It also stopped fitting — GitHub caps an asset at 2 GB, the
> content-addressed corpus is 12.36 GB.
>
> Sections further down that describe publishing or pulling `3gpp.duckdb.zst`
> from the release describe the **retired** flow. They are kept because the
> reasoning about volumes, deltas and clobbering still applies — only the channel
> changed.

## The model: binaries roll on `latest`, the corpus lives in a private package

There is **exactly one GitHub Release, tagged `latest`**. It holds the current
binaries and small **metadata** only. Assets are replaced in place (clobbered);
the release is never deleted, old artifacts never accumulate — no version
history, no disk-space creep.

The corpus is pushed by `scripts/local/publish-corpus.sh` to
`ghcr.io/<owner>/3gpp-corpus`, a **private** package, as one layer holding
`/3gpp.duckdb`.

```
GitHub Release `latest` (the only one) — PUBLIC, carries no clause text
├── mcp-3gpp_linux_amd64.tar.zst   (+ .sha256)   ← Release workflow, on push to main
├── mcp-3gpp_darwin_arm64.tar.zst  (+ .sha256)   ← Release workflow
└── corpus-index.json                            ← the delta anchor:
                                                    spec|release → highest indexed
                                                    version. A version list, no text.

ghcr.io/<owner>/3gpp-corpus:latest — PRIVATE, one layer = /3gpp.duckdb
ghcr.io/<owner>/etsi-corpus:latest — PRIVATE, one layer = /etsi.duckdb

Client:  mcp-3gpp bootstrap → GHCR manifest → layer (Range-resumable,
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
| Indexed DB `3gpp.duckdb` | ~1.7 GB (≈0.8 GB zstd) | Release `latest` asset | ❌ |
| BGE-M3 / reranker / ONNX RT | ~2.3 GB | HuggingFace (fetched by `bootstrap --semantic`) | ❌ |

The corpus never enters git or a Release as raw files. Models stay on
HuggingFace (`model.onnx_data` is 2.2 GB — over the 2 GB Release-asset cap
anyway). The DB compresses under the 2 GB cap.

## Workflows

| Workflow | Trigger | Does |
|---|---|---|
| `ci.yml` | push/PR `main` | gofmt + vet + `go test -race` matrix {ubuntu, macos}. Lint = ktn-linter (hooks), not golangci-lint. |
| `release.yml` | push `main` (code paths) + manual | Build binaries natively per-OS → `gh release upload latest … --clobber`. |
| `corpus-sync.yml` | cron + manual | **C4 (stub)**: refresh the DB and clobber it onto `latest`. |

### Corpus Sync (C4) — incremental, DB-as-state

The hard constraint: a hosted runner has ~14 GB disk / 7 GB RAM, so the ~20 GB
corpus is **never** fully re-ingested in CI. Each run is a delta:

1. Pull the current `3gpp.duckdb.zst` from `latest`.
2. Diff the live 3gpp.org listing against what the DB already contains.
3. Download **only the missing specs** → convert (LibreOffice) → `ingest --append`.
4. `gh release upload latest 3gpp.duckdb.zst --clobber` (single asset replaced;
   the old one is gone).

Kept deliberately simple: "download what's missing, append, re-publish." No
ghcr mirror, no dated releases, no elaborate delta engine. Blocked only on an
`ingest --append` path in `cmd/ingest` (next coding phase). A full rebuild,
when ever needed, runs locally / on a large runner — never on hosted CI.

## Client experience

```bash
curl -fsSL https://raw.githubusercontent.com/kodflow/3gpp-mcp/main/scripts/install.sh | sh
mcp-3gpp bootstrap     # pulls latest DB (+ --semantic for models), sha256-verified
mcp-3gpp serve         # MCP over stdio, offline from here
```

`install.sh` and `bootstrap` resolve `releases/latest/download/…` — a stable URL
that always points at the one current version.

### Auto-update: the `.sha256` is the version signal

The DB has no version number — its **sha256 is its identity**. `corpus-sync`
publishes `3gpp.duckdb.zst` plus `3gpp.duckdb.sha256` (hash of the *decompressed*
DB). At `serve` **startup** (never at query time — local-first):

1. Best-effort GET the tiny `3gpp.duckdb.sha256` from `latest`.
2. Compare to the hash of the cached DB.
   - differs / no cache → pull the new `3gpp.duckdb.zst`, verify, atomic swap;
   - same → use cache, **no download** (the 0.8 GB moves only when it changed);
   - offline / fetch error → keep the cached DB (degrade-don't-block).
3. Opt-out via `--no-update` / `MCP3GPP_NO_UPDATE=1` (air-gapped).

`internal/bootstrap.Fetch` already skips the download when the cached file's
sha256 matches the `Artifact.SHA256`, so this is a thin `CheckAndUpdateDB` on
top.

## Open items

- Implement `ingest --append` + wire `corpus-sync.yml` (C4).
- Wire `bootstrap`'s default DB URL to `releases/latest/download/3gpp.duckdb.zst`
  so `--db-url` is optional.
- First publish of the DB asset (seed `latest` from the existing local
  `data/3gpp.duckdb`).
