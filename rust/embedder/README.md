# rust/embedder — optimised BGE-M3 dense embedder

The expensive part of building the corpus — the ONNX forward pass + tokenisation —
runs here, in Rust, driving [`ort`](https://github.com/pykeio/ort) (ONNX Runtime) +
[`tokenizers`](https://github.com/huggingface/tokenizers) directly. (fastembed 4.9.1
ships no BGE-M3 and can't load its >2GB external-data ONNX from a single buffer, so we
load `model.onnx` from a file path — ORT reads the `model.onnx_data` sibling
automatically — tokenise with the model's tokenizer.json, take the CLS token, then
L2-normalise.) Everything that touches DuckDB stays in Go, which owns the exact DuckDB
version that wrote the corpus.

The two sides exchange JSONL, so this binary **never opens the database** — which
removes any DuckDB storage-format compatibility risk between the runtimes and keeps the
Kaggle binary small.

## GPU

`ort` is built with the `cuda` feature; execution providers are tried **CUDA → CPU**.
The same binary uses the GPU where CUDA is present (Kaggle T4 — the throughput that lets
Rust replace the Go GPU embed) and falls back to CPU on CI/local, with no build-time GPU
requirement.

## Why BGE-M3 (and not a smaller model)

The project mandates dense **and** sparse retrieval (+ a reranker). BGE-M3 is the one
model that emits dense (1024) + sparse + ColBERT, so it stays the unified backbone; the
speed win comes from this Rust runtime (ORT + GPU + batching), not from swapping to a
smaller dense-only model (EmbeddingGemma-300M / Qwen3-Emb-0.6B would force a separate
SPLADE model for sparse). This crate produces the **dense** vectors; the sparse arm
stays the proven Go path (`cmd/embed --sparse-only`, serve-matched).

## Contract

```text
work-list (--in) : {"chunk_id":U64,"heading":S,"text":S}        # one JSON object / line
vectors  (--out) : {"chunk_id":U64,"hash":S,"vec":[f32;1024]}   # one JSON object / line
```

`hash` is byte-for-byte the Go `internal/embed.ClauseHash`
(`sha256(heading+"\n"+text + "|" + embed_identity)[:16]`); `embed_identity` is whatever
`go run ./cmd/embedid` prints. Writing the matching hash means a later Go `cmd/embed`
re-check is a no-op (no re-embed churn).

`--out` doubles as the **resume ledger**: chunk_ids already present are skipped and new
vectors appended, so a killed run resumes instead of restarting. `--limit` bounds a
session. A live progress bar (count, %, ETA, throughput) is printed to stderr.

## End-to-end (campaign flow)

```bash
# 1. Go exports the work-list from the DuckDB (read-only)
go run ./cmd/embed-io --db data/3gpp.duckdb --export-worklist work.jsonl --resume
ID=$(go run ./cmd/embedid)

# 2. Rust embeds (GPU on Kaggle; CPU works too). Resumable, bounded by --limit.
cargo run --release -- \
  --in work.jsonl --out vecs.jsonl \
  --model-dir /models/bge-m3 --embed-identity "$ID" --batch 64

# 3. Go imports the vectors and (re)builds the HNSW
go run ./cmd/embed-io --db data/3gpp.duckdb --import-vectors vecs.jsonl \
  --embed-identity "$ID" --build-hnsw
```

The `--model-dir` holds the BGE-M3 ONNX export: `model.onnx` (name overridable via
`--onnx`), its external-data sibling `model.onnx_data` (loaded automatically by ORT via
the relative path baked into the graph), and `tokenizer.json`. These are the
`onnx/model.onnx`, `onnx/model.onnx_data` and `tokenizer.json` files from the
`BAAI/bge-m3` HF repo.

## Develop

```bash
cargo fmt --check
cargo clippy --all-targets -- -D warnings
cargo test
```

CI (`.github/workflows/rust-embedder.yml`) runs all of the above + a release build.
