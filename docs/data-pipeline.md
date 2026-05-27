# Data Pipeline & Distribution

> How 3GPP sources become the indexed DB ("R"), and how the `mcp-3gpp` binary
> gets it — without ever putting the corpus in git, and without accumulating
> versions.

## The model: one rolling `latest`, the DB is the state

There is **exactly one GitHub Release, tagged `latest`**. It holds the current
binaries **and** the current indexed DB. Assets are replaced in place
(clobbered); the release is never deleted, old artifacts never accumulate — no
version history, no disk-space creep.

```
GitHub Release `latest` (the only one)
├── mcp-3gpp_linux_amd64.tar.zst   (+ .sha256)   ← Release workflow, on push to main
├── mcp-3gpp_darwin_arm64.tar.zst  (+ .sha256)   ← Release workflow
└── 3gpp.duckdb.zst                (+ .sha256)   ← Corpus Sync cron (C4)

Client:  mcp-3gpp bootstrap → pulls releases/latest/download/3gpp.duckdb.zst
                              (stable URL, sha256-verified) → serve (offline)
```

**Why no versions:** the user wants a single frozen `latest` with old ones
deleted to avoid disk abuse. The DB itself records what specs/versions are
indexed, so the DB *is* the state — no manifest history needed.

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

## Open items

- Implement `ingest --append` + wire `corpus-sync.yml` (C4).
- Wire `bootstrap`'s default DB URL to `releases/latest/download/3gpp.duckdb.zst`
  so `--db-url` is optional.
- First publish of the DB asset (seed `latest` from the existing local
  `data/3gpp.duckdb`).
