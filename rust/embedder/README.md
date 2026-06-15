# rust/embedder — optimised BGE-M3 dense embedder

The expensive part of building the corpus — the ONNX forward pass + tokenisation —
runs here, in Rust (via [`fastembed`](https://github.com/anush008/fastembed-rs), which
wraps ONNX Runtime + HF tokenizers and ships a dedicated BGE-M3 path). Everything that
touches DuckDB stays in Go, which owns the exact DuckDB version that wrote the corpus.

The two sides exchange JSONL, so this binary **never opens the database** — which
removes any DuckDB storage-format compatibility risk between the runtimes and keeps the
Kaggle binary tiny (model + tokenizer only, no bundled C++ engine).

## Why BGE-M3 (and not a smaller model)

The project mandates dense **and** sparse retrieval (+ a reranker). BGE-M3 is the one
model that emits dense (1024) + sparse + ColBERT from a single forward pass, so it stays
the unified backbone; the speed win comes from this Rust runtime (ORT + batching, int8
optional), not from swapping to a smaller dense-only model (EmbeddingGemma-300M /
Qwen3-Emb-0.6B would force a separate SPLADE model for sparse). Dense lands in P3 (this
crate); the sparse arm is added in P4.

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

The `--model-dir` holds the standard HF/ONNX files: `model.onnx` (name overridable via
`--onnx`), `tokenizer.json`, `config.json`, `special_tokens_map.json`,
`tokenizer_config.json`.

## Develop

```bash
cargo fmt --check
cargo clippy --all-targets -- -D warnings
cargo test
```

CI (`.github/workflows/rust-embedder.yml`) runs all of the above + a release build.
