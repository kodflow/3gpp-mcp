# Data Pipeline & CI/CD Architecture

> How 3GPP sources, the intermediate corpus, the RAG index ("R"), and the
> models flow through GitHub — without ever putting 74 GB in git.

This document is the answer to the central design question: **where does the
data live, and how does CI turn raw 3GPP specs into a distributable index that
the `mcp-3gpp` binary pulls on demand.**

---

## 0. The volumes we are dealing with

| Asset | Source | Size | Mutability |
|---|---|---|---|
| 3GPP DOCX/ZIP (origin) | `3gpp.org/ftp/Specs/archive` | ~37 GB | grows each TSG meeting |
| Converted HTML (LibreOffice) | derived from origin | ~37 GB | derived, deterministic |
| **Index `3gpp.duckdb` ("R")** | derived from HTML | ~1.7 GB (≈0.8 GB zstd) | rebuilt per release |
| BGE-M3 / reranker / ONNX RT | HuggingFace + GitHub | ~2.3 GB | pinned, rarely changes |

The hard fact: **74 GB of corpus cannot live in git, cannot fit on a single
GitHub-hosted runner (~74 GB usable on `/mnt`, not enough for origin+HTML at
once), and Git LFS bills storage *and* bandwidth even on public repos.**

---

## 1. Storage tiering (the decision)

| Tier | What | Where | Cost on public repo |
|---|---|---|---|
| **Code + manifest** | Go source, `corpus.lock`, model pins | **Git** | free, stays < 50 MB |
| **Raw + converted corpus** | origin ZIPs, converted HTML | **ghcr.io (OCI via ORAS)** | **free, unmetered** |
| **Index "R"** | `3gpp.duckdb` (zstd) + `SHA256SUMS` | **GitHub Release asset** | free, unmetered bandwidth |
| **Models** | BGE-M3, reranker, ONNX Runtime | **HuggingFace + GitHub** (fetched by bootstrap) | free |

### Why ghcr.io and not Git LFS

Git LFS on a **public** repo still consumes the metered LFS storage + bandwidth
quota (1 GB free, then paid data packs), and every CI checkout that pulls LFS
objects burns bandwidth and bloats clone time for everyone. ghcr.io OCI
artifacts are **free and unmetered for public packages**, pulled over GitHub's
internal network (no 3gpp.org rate-limit), content-addressed, and decoupled
from git history. For a 74 GB mirror that CI re-reads constantly, ghcr wins on
every axis.

### Why GitHub Releases for the index

A Release asset is capped at **2 GB/file** (the zstd-compressed DuckDB is
< 1 GB — fits) with **unmetered download bandwidth**. The binary already speaks
this protocol: `mcp-3gpp bootstrap` downloads a release URL, verifies SHA-256,
decompresses `.zst`, and caches in `~/.cache/mcp-3gpp/`. We just point it at the
latest `index-*` release.

### Why models stay on HuggingFace

`scripts/fetch-model.sh` already pulls BGE-M3 from HuggingFace and ONNX Runtime
from GitHub releases, pinned by version. `model.onnx_data` is 2.2 GB — over the
Release 2 GB asset cap anyway — so re-hosting buys nothing. Bootstrap fetches
models from their upstream, pinned.

---

## 2. The `corpus.lock` manifest

The single git-tracked artifact that makes the whole pipeline reproducible and
incremental. One row per spec version, content-addressed.

```jsonl
{"spec_id":"33.128","release":"Rel-19","version":"19.6.0","series":"33","doc_type":"TS","origin_url":"https://www.3gpp.org/ftp/Specs/archive/33_series/33.128/33128-j60.zip","sha256":"<zip sha>","oci_ref":"ghcr.io/kodflow/3gpp-mcp/corpus:33.128-19.6.0","status":"converted"}
```

- **`origin_url` + `sha256`** → reproducibility: a build pins exactly which
  bytes were ingested.
- **`oci_ref`** → where the converted HTML lives in ghcr (the durable mirror).
- **`status`** → `pending` (seen on 3gpp.org, not yet fetched) | `converted`
  (HTML in ghcr) | `failed` (LibreOffice degraded — see `.degraded.tsv`).

`corpus-sync` diffs the live 3gpp.org listing against this file to decide what
to download. `build-index` reads it to know what to pull from ghcr. Bumping the
lockfile (via PR) is the audit trail of every corpus change.

---

## 3. The pipelines

```
                         ┌──────────────────────────────────────────┐
   3gpp.org/ftp  ───────▶│  corpus-sync.yml   (scheduled + manual)  │
   (deltas only)         │  diff lock → download new → soffice HTML │
                         │  → push HTML to ghcr → PR: bump lock      │
                         └──────────────────────────────────────────┘
                                          │  (corpus.lock merged)
                                          ▼
                         ┌──────────────────────────────────────────┐
   ghcr.io/...corpus ───▶│  build-index.yml   (on lock change/tag)  │
   (HTML, streamed)      │  stream HTML per-spec → cmd/ingest        │
                         │  → 3gpp.duckdb → `make bench` gate        │
                         │  → zstd + sha256 → GitHub Release         │
                         └──────────────────────────────────────────┘
                                          │  (index-YYYY.MM.DD release)
                                          ▼
                         ┌──────────────────────────────────────────┐
   HuggingFace models ──▶│  mcp-3gpp bootstrap  (client side)       │
                         │  pull latest index release + verify SHA  │
                         │  + fetch models → serve over stdio        │
                         └──────────────────────────────────────────┘

   go-ci.yml (every PR): fmt · vet · golangci-lint · test -race · build
```

### 3.1 `go-ci.yml` — code quality (GitHub-hosted, fast)

Migrated from `.gitlab-ci.yml`. Runs on every PR/push: `gofmt`, `go vet`,
`golangci-lint`, `go test -race -coverprofile`, and a CGO build of
`cmd/server` + `cmd/ingest`. No corpus needed. Minutes ~free (public repo).

### 3.2 `corpus-sync.yml` — incremental mirror (GitHub-hosted, streaming)

- **Trigger**: weekly cron + `workflow_dispatch` (with `series`/`release`
  inputs for targeted runs).
- **Per delta spec only** (peak disk stays small):
  1. download ZIP from 3gpp.org,
  2. `soffice --headless` → HTML,
  3. `oras push ghcr.io/kodflow/3gpp-mcp/corpus:<spec>-<ver>` the HTML,
  4. record `sha256` + `oci_ref` + `status` in `corpus.lock`.
- **Output**: a PR bumping `corpus.lock`. Sources never touch git.
- **First full bootstrap** (~19k specs, hours of LibreOffice) is **sharded by
  series across a matrix** so each job stays under the 6 h job limit; steady
  state runs only process the handful of new versions per TSG meeting.

### 3.3 `build-index.yml` — build & publish the "R" (GitHub-hosted, streaming)

- **Trigger**: `corpus.lock` change on main + `workflow_dispatch` + release tag.
- Streams HTML from ghcr **spec by spec** (pull → parse → insert → drop) so
  disk never approaches 37 GB; the DuckDB grows incrementally to ~1.7 GB.
- Runs `cmd/ingest` (+ optional `-tags onnx` embedding pass), then
  `cmd/ingest-catalog` and `cmd/ingest-openapi` overlays.
- **Quality gate**: `make bench` (offline IR metrics — Recall@k / nDCG@k).
  A regression below threshold fails the build → no bad index ships.
- Compresses (`zstd -19`), writes `SHA256SUMS`, and publishes to a
  **GitHub Release** tagged `index-YYYY.MM.DD`, marked latest.

### 3.4 `index-release.yml` — binaries (GitHub-hosted matrix)

On a version tag, cross-compile CGO binaries (`linux/amd64`, `linux/arm64`,
`darwin/arm64`) and attach them to the release next to the index. The shipped
binary defaults its bootstrap URL to the repo's latest `index-*` release.

---

## 4. Client experience (the payoff)

```bash
go install github.com/kodflow/3gpp-mcp/cmd/server@latest   # or download binary
mcp-3gpp bootstrap        # pulls latest index release + models, verifies SHA
mcp-3gpp serve            # MCP over stdio, offline from here on
```

No 74 GB download, no LibreOffice, no rebuild. The user gets the ~0.8 GB index
+ models and serves immediately. `bootstrap` re-run pulls a newer index when one
ships.

---

## 5. Reproducibility contract

Same `corpus.lock` + same `cmd/ingest` version ⇒ same `3gpp.duckdb` hash. The
lockfile pins origin bytes by SHA-256; ghcr refs are immutable; embeddings use
pinned model weights. `make bench` proves retrieval quality did not regress
before any index is published.

---

## 6. Open items for the migration phase (not this scaffolding pass)

- Rename Go module `github.com/kodflow/3gpp-mcp` →
  `github.com/kodflow/3gpp-mcp`.
- Add a `cmd/corpus-sync` (or extend `scripts/corpus.sh`) to emit/consume
  `corpus.lock` and push/pull ghcr via `oras`.
- Point `internal/bootstrap` default URL at the GitHub Release.
- Decide embedding strategy in CI (CPU-only `-tags onnx` pass vs. ship
  lexical-only index first, add vectors in a second release).
