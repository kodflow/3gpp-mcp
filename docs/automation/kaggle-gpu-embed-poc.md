# Kaggle T4 GPU embed POC (operator-driven)

> **Status: best-effort manual acceleration path, NOT a CI base.** Kaggle
> sessions are ephemeral (~12 h), throttled, and bound by Kaggle's ToS. The
> actual GPU run is **operator-driven** from a laptop — nothing here runs in CI.
> Provider-agnostic CI integration is the separate follow-up (PR-11b).

## What this proves

CPU embedding of the corpus runs at ~0.33 clauses/s, so a full vectorisation is
an offline, multi-day job. This POC vectorises a small lexical `3gpp.duckdb` on
Kaggle's free **T4** GPU and pulls back a valid semantic DB, driven entirely via
the Kaggle public API (no notebook clicking), to measure the real T4 speedup.

## The only code it needs

The ONNX Runtime execution provider is now **selectable**:

```text
ORT_EP=cpu   (default)  → CPU EP, no SessionOptions, no GPU lib required
ORT_EP=cuda             → builds SessionOptions + AppendExecutionProviderCUDA(device 0)
```

- Selection lives in `internal/embed/embed.go` (`embed.ExecutionProvider()`),
  tag-free and unit-tested (`ep_test.go`) — no GPU needed to test the logic.
- The CUDA wiring (`sessionOptionsFor`) is in `internal/embed/embed_onnx.go`
  (build tag `onnx`). It **compiles on any host**; it only succeeds at runtime
  on a GPU box with the CUDA-enabled ORT shared library. On any failure the
  embedder degrades to `Disabled{}` (degrade, never block).

## How to run it (laptop)

Prereqs: the `kaggle` CLI (`pip install kaggle`) with credentials in
`~/.kaggle/kaggle.json` (chmod 600) **or** `KAGGLE_USERNAME`/`KAGGLE_KEY`. The
driver never reads, copies, or uploads those — auth is the CLI's job. The
dataset is built with `git archive` (tracked files only) so the corpus (`data/`)
and any credential file can never be staged or uploaded.

```bash
# 1. Build a small lexical DB (one real series), fetch the model + the CUDA ORT lib.
./bin/ingest --series 33 --release Rel-15 --resume            # → data/3gpp.duckdb
scripts/fetch-model.sh                                        # → data/models/bge-m3
#    (obtain a CUDA-enabled libonnxruntime.so matching Kaggle's CUDA → $ORT_GPU_LIB)

# 2. Stage inputs, push the dataset, run the kernel, pull the embedded DB back.
export KAGGLE_USER=<your-handle>
export ORT_GPU_LIB=/path/to/onnxruntime-gpu/lib/libonnxruntime.so
scripts/kaggle-embed-poc.sh all     # = assemble + dataset + push + status(poll) + output
```

The kernel body is `scripts/kaggle/kernel-embed.sh`: on the T4 it builds
`cmd/embed` with `-tags onnx`, runs `ORT_EP=cuda embed --embed-floor Rel-15
--require-semantic --report json`, asserts `null_embeddings_at_floor==0`, and
prints `clauses/s`.

## Validate locally (after pulling the DB back)

- vectors present; HNSW index frozen
- a sample vector search returns sensible neighbours
- a handful of GPU vectors match a CPU-fp32 reference within tolerance
  (cosine ≈ 1.0)

## fp16 caveat (feeds PR-6 / EmbedIdentity)

A GPU **fp16** run produces slightly different vectors than CPU **fp32**.
Measure the cosine delta during validation and **decide**: either standardise
the semantic channel on one precision (and re-embed once), or fold precision
into `EmbedIdentity` so fp16-GPU and fp32-CPU vectors are never mixed in the
same HNSW. Do **not** publish a corpus that silently mixes precisions.

## Acceptance (POC)

- GPU EP confirmed active in the kernel logs (`execution provider = cuda`)
- returned DB fully vectorised at/above the floor (`null_embeddings_at_floor==0`)
  with the HNSW frozen
- GPU vectors compatible with the CPU reference within tolerance
- throughput **documented** (target ≈ 50× the CPU baseline — not a hard fail, so
  Kaggle throttling / dataset I/O can't fail the POC)
- the whole thing driven from the laptop via the Kaggle API
