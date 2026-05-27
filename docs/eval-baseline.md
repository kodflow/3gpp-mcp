# Retrieval evaluation — baseline & methodology (axis A5)

## Harness

`make bench ARGS="-db <db> -set <queries.json>"` runs `cmd/bench` over a graded
query set (`internal/eval`), reporting nDCG@5/@10, Recall@10, MRR@10, Success@1
for three systems: **lexical (BM25)**, **hybrid (RRF)**, **hybrid + rerank**.
Embedder/reranker are selected by `EMBEDDER` / `RERANKER` env (see `internal/embed`,
`internal/rerank`). Default set: `docs/inputs/eval/li_5gc_queries.json`.

## Measured results

### Semantic uplift (BGE-M3 real vectors)
On a vector-bearing DB (33.128, ~565 clauses, real BGE-M3 dense vectors), the
hybrid path beat lexical by a clear margin:

| system | nDCG@10 | Success@1 |
|---|---|---|
| lexical (BM25) | 0.356 | 0.167 |
| hybrid (BM25 + BGE-M3) | **0.466** | **0.333** |

Exact-clause-first doubled (0.167 → 0.333). This is the value of the dense arm
on natural-language / paraphrased queries (the case where keyword overlap is low).

### Embedder correctness (fresh, this session)
`go test -tags onnx ./internal/embed` (`TestBatchMatchesSingle`) asserts on the
real model that a **batched** embedding equals a **solo** one (cos ≥ 0.999) and is
unit-norm — i.e. padding + attention_mask are correct. Semantic sanity:
`cos(LI-paraphrase, LI-clause) = 0.585` vs `cos(LI, unrelated NR-PHY) = 0.279`.

## Cost finding (drives the distribution design)

BGE-M3 is a 560M-param model; on CPU, embedding is **compute-bound**, ~1 clause/s
on this 8-core host even at batch=32. The full corpus (~858 k clauses) is therefore
**days** of CPU — embedding MUST be an offline/GPU batch step that produces the
published `3gpp.duckdb` artifact. The server only ever *reads* vectors; it never
embeds the corpus at runtime. This is exactly why the binary downloads a
pre-indexed DB (see `docs/install.md`) rather than building it.

Consequence for CI: the regression bench runs on a **small vector-bearing fixture
DB** (a few specs, embedded once and committed as a test artifact), never a full
re-embed. Wiring this gate is a CI (part C) task.

## Sparse vectors (axis A4) — deferred

BGE-M3 can emit sparse (lexical-weight) and ColBERT vectors in addition to dense.
The ONNX export in use (`BAAI/bge-m3` `onnx/model.onnx`) exposes **only**
`sentence_embedding` (dense) — no sparse head. Adding the sparse arm needs a
different export that surfaces the sparse output; deferred until/if that export is
produced offline. Dense + BM25 hybrid already covers the V1 target.
