# Serving on CPU (8-core / 32 GB) — tuning, deploy & the anti-stale-layer machinery

This guide is the operational counterpart to the retrieval engine: how to run
`3gpp-mcp` well on a **CPU-only box (8 cores, 32 GB RAM, no GPU)**, how to deploy
a new image, and how the build/CI machinery now prevents the *stale-data-layer*
incident from recurring.

## TL;DR — the runtime profile for 8c/32G

Set these on the served container (env). All are opt-in; unset = safe defaults.

| Env | Recommended | Why |
|---|---|---|
| `ORT_INTRA_OP_THREADS` | `6` | Per-query BGE-M3 parallelism. 6 of 8 cores for the forward pass, leaving headroom for DuckDB + the runtime. The single biggest query-embed latency dial on CPU. |
| `ORT_INTER_OP_THREADS` | `1` | Cross-op parallelism; 1 is best for a single short query. |
| `RERANK_ALL` | `1` | Always-on cross-encoder rerank — the top precision lever (quality is the priority). |
| `RERANK_WINDOW` | `10` | Rerank the top-10 fused candidates. On CPU each candidate is a cross-encoder pass, so 10 balances precision vs p99. Raise to 20 if latency budget allows. |
| `EMBED_QUERY_CACHE` | `2048` | LRU of query→vector (≈8 MiB). Repeated/probe/popular queries skip the forward pass entirely. Zero quality loss. |
| `EMBEDDER` / `RERANKER` | *(unset)* | The full image is an ONNX build with the models baked; unset = use them. Set `off` only to force lexical. |

The server prints the effective config at startup:

```
[3gpp-mcp] config: cpus=8 gomaxprocs=8 ort_intra=6 ort_inter=1 rerank_window=10 rerank_all=true query_cache=2048 embedder=true reranker=true
```

> **HNSW fits in RAM.** 2.85 M × 1024 fp16 ≈ 6 GB of vectors + the HNSW graph —
> comfortable on 32 GB. Once the served DB carries a *frozen* HNSW (see below), the
> vector arm is k-NN in ms; the only remaining per-query cost is the embed, which
> the thread knobs + cache address.

## Is my server healthy? (30-second diagnosis)

```bash
curl -s 'https://<host>/dashboard.json?token=<TOKEN>' \
  | jq '{hnsw_state, fts_index_present, data_image_created, source_corpus, reason}'
```

Healthy looks like:

```json
{ "hnsw_state": "frozen", "fts_index_present": true,
  "data_image_created": "2026-…", "source_corpus": "sha256:…", "reason": "" }
```

If you see `hnsw_state` ≠ `"frozen"` or `fts_index_present: false`, the server is
serving a **stale / unindexed data layer** — BM25 falls back to a LIKE full-scan
(catastrophic p99) and the vector arm to bounded exact-scan (reduced recall). The
same condition is shouted in the container logs at startup:

```
[3gpp-mcp] ⚠️  DEGRADED DATA LAYER — fts_index=… hnsw_state="" … Fix: rebuild mcp:full FROM an indexed 3gpp-data digest …
```

## Deploy a fresh image (and fix a degraded prod)

The mcp image inherits its ~14 GB data layer **FROM `3gpp-data` by digest pinned at
mcp-image build time**. A correct, indexed `3gpp-data` can exist on the registry
while an old, unindexed layer is still baked into the running `3gpp-mcp:full`.
Fixing it is a redeploy, not a rebuild:

```bash
docker pull ghcr.io/<owner>/3gpp-mcp:latest
docker rm -f 3gpp-mcp           # or: docker compose up -d --force-recreate
# The 8c/32G profile above is shipped as a ready env-file:
docker run -d --name 3gpp-mcp \
  --env-file deploy/labs-8c32g-env.conf \
  -p 8765:8765 ghcr.io/<owner>/3gpp-mcp:latest
```

Then re-run the diagnosis curl — expect `hnsw_state:"frozen"`, `fts_index_present:true`.

The `Corpus Data Image` → `corpus-image` run posts a **deploy summary** (job
summary) with the exact published digest + inherited data provenance + this action,
so each publish tells you precisely what to pull.

## Why this can't silently recur

Three structural guards, all in CI/build:

1. **Build-time inheritance guard** — the `full` image runs
   `mcp-3gpp check-data --require-fts --require-hnsw` against the inherited data
   layer as a `RUN` step. An unindexed layer **fails `docker build`** instead of
   shipping a degraded server.
2. **Recipe-hash rebake trigger** — `scripts/bake-recipe-hash.sh` hashes the files
   that define *how* the DB is baked (freeze-hnsw, overlay, merge, FTS/HNSW store
   logic, `Dockerfile.data`, the workflow). It's stamped into `3gpp-data` as
   `io.kodflow.3gpp.bake.recipe`; the daily change-guard rebakes when the recipe
   changes — not only when the corpus content moves. This is exactly what was
   missing when `freeze-hnsw` was added but the guard kept skipping.
3. **Provenance everywhere** — the data labels (`created`, `source.corpus`) are
   forwarded into the mcp image env and surfaced on `/dashboard.json`, so "which
   layer am I serving?" is a curl, not a CI dig.

## Quality regression gate (IR metrics)

`retrieval-regression.yml` scores the graded query set with `cmd/bench` on every PR
touching `internal/search|store|rerank` or `cmd/bench`, comparing nDCG@5/@10,
Recall@10, MRR@10, Success@1 to a committed baseline within a tolerance.

**It is advisory until the baseline is seeded** (one-time, keeps the gate honest):

```bash
# run the workflow once (workflow_dispatch), then:
gh run download <run-id> -n retrieval-metrics
mv retrieval-metrics.json docs/inputs/eval/baseline.json
git add docs/inputs/eval/baseline.json && git commit -m "test(eval): seed retrieval baseline"
```

From then on, any tracked-metric drop > tol fails the PR. (CI scores `lexical` only
— it has no vectors; hybrid/rerank quality is validated locally with the ONNX build:
`make bench EMBEDDER=onnx RERANKER=onnx`.)

## Follow-up: the sparse arm (needs a re-embed — not a serve change)

BGE-M3 also emits **sparse** (lexical-semantic) weights, excellent for 3GPP
terminology (IE names, NF acronyms, spec ids). Activating a sparse arm is **not** a
serve-side toggle: sparse vectors are **not in the baked DB**, so it requires
regenerating embeddings across the corpus (a bake-pipeline change + a full
re-embed) before the store/search can fuse dense + sparse + BM25. Track it as a
dedicated bake variant; do not expect it from the current data layer.
