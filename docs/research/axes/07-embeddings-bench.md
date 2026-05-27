# Axis 07 — Embeddings + Reranker Benchmark (V2 semantic stack)

> Research date: 2026-05-26. Scope: pick the V2 dense-retrieval model and the cross-encoder
> reranker for `3gpp-mcp` **from data**, not priors. Output is an implementation-ready
> design: an eval harness built from our own clause data, a model matrix, the reranker
> integration point, CPU/latency budget, a step-by-step plan, and risks.
>
> Anchors in this repo:
> - Fusion already implemented: `search.RRF(60, ...)` in `internal/search/search.go` — RRF k=60.
> - Vector path already implemented: `store.SearchVectors` (HNSW cosine, `FLOAT[1024]`) in
>   `internal/store/store.go`; embeddings column is `clauses.embedding FLOAT[1024]`.
> - Embedder seam: `internal/embed/` (`Embedder` interface, `Disabled{}`, `Local{}`, ONNX behind
>   `-tags onnx`). `embed.Dim = 1024`.
> - Ground-truth LI events: `docs/inputs/sentinel_r17_events.json` — `(nf, event, spec, clause)`
>   tuples we convert directly into eval queries with known-relevant clause refs.
>
> This axis does **not** change the frozen architecture (`CLAUDE.md` §2/§13): Go-only query
> path, ONNX Runtime + BGE-M3 already named, no Ollama on the query path. A model swap to
> Qwen3-Embedding stays inside "ONNX Runtime (CGO)" and would need an `arch-change`-labelled MR
> only if it displaces BGE-M3 as the documented default. The reranker is already "optional V2"
> in `CLAUDE.md` §3.

---

## 0. TL;DR decision frame

| Question | Default going in | What the bench must prove to change it |
|---|---|---|
| Which dense model? | **BGE-M3** (already named, 1024-dim, emits dense+sparse+ColBERT in one pass) | Qwen3-0.6B beats BGE-M3 dense by ≥ +0.03 nDCG@10 on our LI/5GC set **and** stays inside CPU budget |
| Reranker? | **bge-reranker-v2-m3**, top-20 → top-5, after RRF | ≥ +0.05 nDCG@5 / +0.05 MRR over RRF-only at ≤ ~80 ms/query added |
| RRF k? | **k=60** (Cormack 2009, current code) | A sweep over k∈{20,40,60,80,100} shows >0.01 nDCG gain elsewhere — expected: flat, keep 60 |
| Output dims? | **1024** (matches schema + HNSW) | Matryoshka 512/256 holds recall within −0.01 while cutting index RAM ~2–4× |

Expectation from the literature (to be confirmed, not assumed): the reranker is the **single
biggest precision win**; the dense-model choice is a smaller delta; RRF k is effectively flat.
So the bench is ordered to spend effort where the ROI is.

---

## 1. Why a bench at all (the telecom-specific failure mode)

Pure BM25 on 3GPP prose retrieves the right **spec** but frequently the wrong **release/clause
variant**, because:

- Near-duplicate clauses recur across releases (Rel-17 vs Rel-18 of the same `6.2.2.2.x`).
- Acronym collisions: `AMF` = Access and Mobility Management Function (5GC) vs Application
  Management Function (IMS legacy) — `CLAUDE.md` §8 piège 5.
- LI questions are often **paraphrase** queries ("what events does the AMF report over X2")
  that don't lexically match the heading ("Generation of xIRI over LI_X2"). This is exactly
  where dense + a cross-encoder reranker earn their keep.

We must measure these on **our own corpus**, because public MTEB numbers (BGE-M3 multilingual,
Qwen3-0.6B 64.33) say nothing about dense telecom/LI text with heavy acronym overlap.

---

## 2. The eval harness

### 2.1 Principle

The harness is **offline batch** (permitted by `CLAUDE.md` §13) and **read-only** against a
built `data/3gpp.duckdb`. It never touches the query-time server. It can live as a Go test
under a build tag or a `cmd/bench` one-shot (decision left to implementation; both are Go-only
and honour the mono-binary doctrine — the bench is not shipped in `mcp-3gpp`).

### 2.2 Eval-set format

A query set is a JSON array; each entry is a query with one or more **known-relevant clause
refs**. Relevance is graded (2 = exact target clause, 1 = acceptable sibling/parent context,
0 = everything else) so we can compute nDCG, not just hit-rate. `nf`/`domain`/`tags` let us
slice results (5GC vs EPC, X2 vs X3, paraphrase vs lexical).

```jsonc
// docs/inputs/eval/li_5gc_queries.json
[
  {
    "id": "amf-x2-events",
    "query": "what events does the AMF report over X2",
    "intent": "hybrid",                 // expected router class (search.Classify)
    "domain": "5GC",
    "tags": ["paraphrase", "li", "x2", "nf:AMF"],
    "relevant": [
      { "spec_id": "33.128", "clause": "6.2.2.2", "grade": 2 },   // "Generation of xIRI over LI_X2" (AMF)
      { "spec_id": "33.128", "clause": "6.2.2.2.2", "grade": 1 }, // AMFRegistration
      { "spec_id": "33.128", "clause": "6.2.2.2.4", "grade": 1 }  // Location update
    ],
    "release": "Rel-17",                // optional filter to pass into store.SpecFilter
    "notes": "Target POC question. Heading wording differs from query -> tests dense+rerank."
  }
]
```

Matching rule: a returned hit counts as relevant if its `(spec_id, clause_path)` equals a
`relevant` entry **or** is a descendant of one (e.g. `6.2.2.2.2` satisfies a `6.2.2.2` target
at the parent's grade, capped). Keep the rule in one helper so it's consistent across metrics.

### 2.3 How the query set is constructed from OUR clause data (semi-automatic)

We already have the ground truth; we don't invent it.

1. **Seed from `sentinel_r17_events.json`.** Each `(nf, event, spec, clause)` row is a verified
   target. Auto-generate paraphrase queries per row with a small template set, e.g.
   - "what is the {event} event reported by the {nf}" → relevant: that clause (grade 2),
     parent NF X2/X3 section (grade 1).
   - "which {nf} event corresponds to {event human-readable}" .
   This yields ~50–150 candidate queries deterministically. **A human prunes/edits** to ~30–50
   high-quality ones (the file is the source of truth, hand-curated — like the existing inputs).
2. **Add section-level paraphrase queries** ("events the SMF reports over X2" →
   `6.2.3.2` family) — these are the realistic analyst questions and the ones BM25 misses.
3. **Add lexical-control queries** ("TS 33.128 clause 6.2.3.2.2", "PDU session establishment
   xIRI") that BM25 already nails — they guard against a dense model *regressing* easy cases.
4. **Add cross-release disambiguation queries** (same clause, force `release: Rel-17` vs leave
   unfiltered) to measure the wrong-variant failure mode directly.
5. **Add acronym-collision queries** ("AMF registration event" expecting 5GC `33.128`, not an
   IMS-context hit) to measure §8-piège-5 behaviour.

The generator is a throwaway Go script; its **output JSON is committed and reviewed**. We never
let an LLM decide relevance — relevance comes from the spec clause numbers we already trust.

### 2.4 Metrics

Standard IR metrics, computed per query then averaged (macro), and also sliced by `tags`:

| Metric | Definition | Why |
|---|---|---|
| **Recall@k** (k=5,10,20) | fraction of graded-relevant clauses retrieved in top-k | retrieval arm health; gates what the reranker can even see |
| **nDCG@k** (k=5,10) | rank-discounted gain using the 0/1/2 grades | headline quality; rewards putting the exact clause first |
| **MRR@10** | 1/rank of first relevant | "did the right clause come first" — matches analyst UX |
| **Success@1** | top-1 is grade-2 | strict exact-clause hit; the no-hallucination bar |

Report a 95% bootstrap CI over queries so a +0.02 nDCG isn't read as signal when it's noise on
40 queries. Decisions use **paired** comparison (same queries, two systems) → paired bootstrap /
sign test, which is far more sensitive than comparing two independent means.

### 2.5 Systems under test (ablation grid)

Run every model through the **same** pipeline so differences are attributable:

```
A. lexical-only        : store.SearchClauses (BM25)                     [baseline = today]
B. dense-only          : store.SearchVectors (model M)
C. hybrid RRF          : RRF(60, A, B)                                  [current Search() shape]
D. hybrid RRF + rerank : RRF(60, A, B) -> top-20 -> bge-reranker -> top-5
```

Cross M ∈ {BGE-M3 dense, Qwen3-0.6B@1024, Qwen3-0.6B@512, e5/nomic control}. The reranker (D)
is model-independent (it re-scores text pairs), so we test it on the best C.

---

## 3. The model matrix

All candidates are CPU-runnable via **ONNX Runtime** (the stack's `onnxruntime_go` seam),
honouring "no Ollama / no second LLM on the query path". Sizes are the ONNX export footprint
(approx — confirm on download); pick **int8** quantized variants for CPU (≈2–3× faster, negligible
quality loss; **fp16 is *slower* on most CPUs** — Intel AMX/Sapphire-Rapids excepted).

| Model | Params | Dims (MRL) | Max ctx | Hybrid output | ONNX source | Approx ONNX size (int8) | Role in bench |
|---|---|---|---|---|---|---|---|
| **BGE-M3** | ~568M | 1024 (fixed) | 8192 | **dense + sparse + ColBERT** in 1 pass | `onnx-community` / `aapot/bge-m3-onnx` / `gpahal/bge-m3-onnx-int8` | ~0.55 GB | **Default candidate** |
| **Qwen3-Embedding-0.6B** | 0.6B | **32–1024 (Matryoshka)** | **32K** | dense only | `zhiqing/Qwen3-Embedding-0.6B-ONNX` (+ optimum export) | ~0.6 GB | **Challenger** (ctx + MRL) |
| Qwen3-0.6B @512 dims | " | 512 (MRL truncate) | 32K | dense only | same, slice output | same weights | MRL recall/RAM trade-off |
| **e5 / nomic** (e.g. `multilingual-e5-base`, `nomic-embed-text-v1.5`) | ~278M / ~137M | 768 / 768 (nomic MRL) | 512 / 8192 | dense only | optimum ONNX exports | ~0.1–0.3 GB | Lightweight control / floor |

Reranker (cross-encoder, applied after fusion):

| Model | Params | Base | Default max_len | ONNX source | Notes |
|---|---|---|---|---|---|
| **bge-reranker-v2-m3** | ~568M | bge-m3 | **512** (extendable; base supports 8192) | `onnx-community/bge-reranker-v2-m3-ONNX`, `hooman650/...-onnx-o4`, `...-o3-cpu` | sequence-classification head → single relevance logit per (query, passage) pair; sigmoid→[0,1] |

Notes that matter for us:

- **BGE-M3's sparse head can feed the lexical arm.** One model serves both BM25-style sparse
  scoring *and* the HNSW dense path — attractive operationally vs maintaining two models. The
  ColBERT head is *not* needed for V2 (no late-interaction index in DuckDB); ignore it.
- **Qwen3-0.6B wins on context (32K) and MRL.** 3GPP clauses are usually < 1k tokens, so 32K
  rarely binds; MRL (truncate to 512/256) is the real lever — it shrinks the HNSW index RAM
  (Axis 4: index must fit in RAM, allocated outside `memory_limit`). Trade recall for RAM and
  measure it.
- **Reranker max_len = 512.** A 3GPP clause body can exceed 512 tokens → must **truncate the
  passage** (head + heading, or head+tail) before scoring. This truncation policy is itself a
  bench variable (test: heading+first-N-tokens vs first-512).

---

## 4. Reranker integration point (exact)

Today `Engine.Search` (`internal/search/search.go`) does:

```
lex  := store.SearchClauses(... topK)
vec  := store.SearchVectors(... topK)        // when emb.Enabled()
hits := RRF(60, lex, vec)
return hits[:topK]
```

V2 inserts the cross-encoder **between RRF and the truncation to topK**, gated behind the
existing `mode` parameter (`CLAUDE.md` §5 `search_spec` already has `mode='hybrid'|'lexical'|'semantic'`;
add `'rerank'` or a boolean, no new tool):

```
fused := RRF(60, lex, vec)          // already implemented
cand  := fused[:min(20, len(fused))]   // retrieve broad
if reranker.Enabled() && mode == rerank {
    scores := reranker.Score(ctx, r.Text, passages(cand))  // (query,passage) pairs
    reorder cand by scores desc                            // stable; keep citation intact
}
return cand[:min(5, len(cand))]     // return narrow
```

- Pattern: **retrieve broad (top-20 per/after fusion) → RRF → rerank → return top-5.** Confirmed
  by the reranker model card ("rerank top-N from initial retrieval") and Axis 3.
- The reranker lives behind a **new interface in `internal/embed/` or a sibling `internal/rerank/`**,
  mirroring the `Embedder` seam: a `Disabled{}` default + an ONNX impl behind `-tags onnx`, so the
  default mono-binary still builds and degrades visibly (no reranker → return RRF top-k). This keeps
  the non-blocking doctrine (degraded, never blocking).
- **Citations are untouched.** Reranking only reorders `model.SearchHit`s; each still carries its
  `Citation`. No hallucination surface added.
- Passage fed to the reranker = `heading + "\n" + text[:N]` (N tuned for the 512 limit). Truncation
  is on the *reranker input only*; the stored clause/citation is unchanged.

---

## 5. CPU / latency budget

Target: keep `search_spec` interactive (local-first, single dev box, no GPU assumed). Rough
per-query budget on a modern x86 CPU (to be measured, these are planning figures):

| Stage | Cost (int8 ONNX, CPU) | Notes |
|---|---|---|
| Query embedding (1 text) | ~5–20 ms | one forward pass; dominated by tokenization + 1 seq |
| HNSW k-NN (top-20) | ~3 ms | DuckDB VSS, already in `store.SearchVectors` |
| BM25 (top-20) | ~5–30 ms | DuckDB FTS, already live |
| RRF fusion | < 1 ms | pure Go, `search.RRF` |
| **Reranker, 20 pairs** | **~60–120 ms** | 20 cross-encoder forward passes @512 tokens; the bulk of added latency |
| **Total (hybrid+rerank)** | **~80–170 ms** | acceptable for interactive MCP; reranker is opt-in via `mode` |

Budget levers if too slow: rerank top-**10** instead of 20; cap passage length < 512; int8 the
reranker (`-o3-cpu`/`-o4` exports exist); batch the 20 pairs in one ONNX call. **Ingestion-side**
embedding of ~858k → ~10M clauses is the heavier cost (offline, batch 32, hours) — but it's batch,
not query path, so it only affects ingest wall-clock, and Matryoshka-512 halves both index RAM and
embed I/O.

Memory: 1024-dim fp32 HNSW over 10M chunks ≈ 10M×1024×4 B ≈ **41 GB** in RAM — over a laptop
budget. This is the strongest argument to **measure Matryoshka-512** (≈20 GB) and **256** (≈10 GB),
and ties directly into Axis 4 (HNSW must fit in RAM). The bench must report the recall cost of
each truncation so the dims choice is data-driven, not guessed.

---

## 6. RRF k confirmation (cheap, do it once)

`search.RRF(k, ...)` takes k as a parameter, so a sweep is trivial: run system C with
k ∈ {20, 40, 60, 80, 100} over the eval set, plot nDCG@10. Literature (Cormack et al., SIGIR 2009;
subsequent hybrid-search benchmarks) says the optimum is **flat across k∈[40,80]**, with smaller k
favouring head-of-list precision and larger k favouring recall feeding a downstream reranker. Since
we *do* feed a reranker, **k=60 (current) sits in the sweet spot**; expected result is "no change
worth making". Document the curve and move on — do not over-tune (Axis 3 guidance).

---

## 7. Step-by-step plan

1. **Build the eval set** (`docs/inputs/eval/li_5gc_queries.json`).
   - Write a throwaway Go generator that reads `sentinel_r17_events.json` + templates → candidate
     queries; hand-curate to ~40 queries spanning the 5 tag families (§2.3). Commit + review the JSON.
2. **Write the harness** (Go, offline, read-only on `data/3gpp.duckdb`; build-tagged test or `cmd/bench`).
   - Reuse `store.SearchClauses`, `store.SearchVectors`, `search.RRF`. Implement metrics (recall@k,
     nDCG@k, MRR, success@1) + paired bootstrap CI + per-tag slicing.
3. **Stand up embedders behind `-tags onnx`** for each matrix model (BGE-M3 dense, Qwen3-0.6B@1024,
   @512, one e5/nomic control). Embed the eval-relevant subset of clauses (or the full corpus once)
   into a scratch DuckDB per model; build HNSW (`store.BuildHNSW`).
4. **Run the grid** (§2.5): A/B/C per model; pick best dense model from C (nDCG@10, paired test).
5. **Add the reranker** (`internal/rerank/`, ONNX behind tag, `Disabled{}` default). Run D on the
   best C. Measure nDCG@5 / MRR / success@1 delta and added latency.
6. **RRF k sweep** (§6) on the best C. Confirm 60.
7. **Matryoshka dims study**: Qwen3 @1024 vs @512 vs @256 — recall vs index RAM. Or, if BGE-M3 wins,
   note it is fixed-1024 (no MRL) and quantify the RAM cost as-is.
8. **Write the verdict** into this axis doc (decision + numbers) and, if it changes the documented
   default model, open an `arch-change`-labelled MR per `CLAUDE.md` §13.

Phasing: steps 1–2 first (the harness has value even with only lexical A), then 3–6, then 7 as a
follow-up. Gate everything behind build tags so the default mono-binary is never affected.

---

## 8. Expected wins (hypotheses to validate)

- **Reranker is the big lever**: +0.05–0.10 nDCG@5 and a clear MRR/success@1 jump on the
  *paraphrase* and *cross-release* tag slices (where BM25/dense alone pick the wrong variant).
  Little movement on *lexical-control* queries (already easy).
- **Dense model choice is a smaller delta**: BGE-M3 dense ≈ Qwen3-0.6B@1024 on short clauses;
  Qwen3 MRL-512 likely within −0.01 recall of its own 1024 while halving RAM. BGE-M3's free
  sparse head is the tie-breaker (one model, both arms) unless Qwen3 clearly wins quality.
- **RRF k**: flat; keep 60.
- **Net**: hybrid+rerank materially beats today's lexical-only on exactly the LI questions the
  product targets (e.g. "events AMF reports over X2" → `6.2.2.2.*`), with citations intact.

---

## 9. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Eval set too small / overfit → noisy verdict | ~40 queries min, 95% bootstrap CI, paired tests; expand from `sentinel` if a slice is thin |
| Relevance labels biased toward `33.128` clause numbers | Add 5GC/EPC prose + acronym-collision queries (§2.3 steps 4–5) so we don't only reward the LI happy path |
| Reranker 512-token cap truncates long clauses → mis-scores | Bench the truncation policy (heading+head vs head+tail); cap N; this is a tracked variable not a silent default |
| HNSW RAM at 10M×1024 ≈ 41 GB (Axis 4) | Measure Matryoshka 512/256 recall cost; build-then-freeze read-only index; report RAM per option |
| fp16 ONNX *slower* on CPU | Use **int8** quantized exports; never assume fp16 helps on CPU |
| Adding a model swaps the frozen default (BGE-M3) | Only the *default* swap needs an `arch-change` MR; the bench itself is offline/build-tagged and changes nothing shipped |
| LLM-graded relevance would reintroduce hallucination | **Forbidden**: relevance is the spec clause numbers we already trust; no model decides ground truth |
| Reranker/embedder pulls heavy deps into the binary | Keep both behind `-tags onnx` + `Disabled{}` default; default mono-binary unaffected, degrades visibly |

---

## 10. Example eval queries (10 LI/5GC, with expected clause refs)

Derived from `docs/inputs/sentinel_r17_events.json` (all `spec_id` 33.128 unless noted). Grades:
2 = exact target, 1 = acceptable parent/sibling context.

| # | Query | Expected clause(s) | Tags |
|---|---|---|---|
| 1 | what events does the AMF report over X2 | `6.2.2.2` (g2), `6.2.2.2.2`/`6.2.2.2.4` (g1) | paraphrase, x2, nf:AMF, 5GC |
| 2 | AMF registration interception event | `6.2.2.2.2` (g2) | paraphrase, nf:AMF, 5GC |
| 3 | which SMF events are generated over LI_X2 | `6.2.3.2` (g2), `6.2.3.2.2`/`6.2.3.2.3`/`6.2.3.2.4` (g1) | paraphrase, x2, nf:SMF, 5GC |
| 4 | PDU session establishment xIRI from the SMF | `6.2.3.2.2` (g2) | lexical-ish, nf:SMF, 5GC |
| 5 | SMS message interception event in the SMSF | `6.2.5.3` (g2), `6.2.5.4` (g1) | paraphrase, nf:SMSF, 5GC |
| 6 | UDM serving system message LI event | `7.2.2.3.2` (g2), `7.2.2.3` (g1) | paraphrase, nf:UDM, 5GC |
| 7 | AUSF authentication request/response events | `6.1` (g2) | paraphrase, nf:AUSF, 5GC |
| 8 | LMF location report event (LALS) | `7.3.1.4` (g2) | paraphrase, nf:LMF, 5GC |
| 9 | push-to-talk session initiation interception | `7.5.2.2` (g2), `7.5.2` (g1) | paraphrase, nf:PTC, services |
| 10 | MME EUTRAN attach LI event (legacy 4G) | `33.108` `10.5.1.2.1` (g2) | paraphrase, nf:MME, EPC, cross-spec |

(#10 is intentionally `33.108` to test EPC/legacy retrieval and the 5GC-vs-EPC tag slice; #2 and
#1 are the canonical "AMF over X2 → 6.2.2.2.*" target the product must nail.)

---

## Sources

- BGE-M3 model card (dense+sparse+ColBERT, 8192 ctx, 1024-dim): https://huggingface.co/BAAI/bge-m3
- BGE-M3 paper (M3-Embedding, self-knowledge distillation): https://arxiv.org/abs/2402.03216
- BGE-M3 ONNX (all 3 heads, multi-language): https://github.com/yuniko-software/bge-m3-onnx ; https://huggingface.co/aapot/bge-m3-onnx
- BGE-M3 ONNX int8 (CPU): https://huggingface.co/gpahal/bge-m3-onnx-int8
- Qwen3-Embedding-0.6B model card (0.6B, 32K ctx, MRL 32–1024, MTEB 64.33): https://huggingface.co/Qwen/Qwen3-Embedding-0.6B
- Qwen3-Embedding repo + blog: https://github.com/QwenLM/Qwen3-Embedding ; https://qwenlm.github.io/blog/qwen3-embedding/ ; https://arxiv.org/abs/2506.05176
- Qwen3-Embedding-0.6B ONNX: https://huggingface.co/zhiqing/Qwen3-Embedding-0.6B-ONNX
- bge-reranker-v2-m3 model card (0.6B, base bge-m3, 512 default len, seq-classification): https://huggingface.co/BAAI/bge-reranker-v2-m3
- bge-reranker-v2-m3 ONNX (incl. CPU/O3/O4): https://huggingface.co/onnx-community/bge-reranker-v2-m3-ONNX ; https://huggingface.co/hooman650/bge-reranker-v2-m3-onnx-o4 ; https://www.promptlayer.com/models/bge-reranker-v2-m3-onnx-o3-cpu
- RRF original (k=60 robustness): Cormack, Clarke, Büttcher, SIGIR 2009 — https://cormack.uwaterloo.ca/cormacksigir09-rrf.pdf
- RRF k tuning (k∈[40,80] flat; smaller=precision, larger=recall-for-reranker): https://bigdataboutique.com/blog/reciprocal-rank-fusion-how-it-works-and-when-to-use-it ; https://blog.serghei.pl/posts/reciprocal-rank-fusion-explained/
- Embedding quantization on CPU (int8 ≈2–3× faster, negligible quality loss; fp16 often slower on CPU): https://medium.com/nixiesearch/how-to-compute-llm-embeddings-3x-faster-with-model-quantization-25523d9b4ce5 ; https://sbert.net/docs/sentence_transformer/usage/efficiency.html
- Qwen3 vs BGE-M3 multilingual comparison: https://medium.com/@mrAryanKumar/comparative-analysis-of-qwen-3-and-bge-m3-embedding-models-for-multilingual-information-retrieval-72c0e6895413
