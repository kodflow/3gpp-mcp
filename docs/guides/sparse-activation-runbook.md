# Sparse arm activation — push-button runbook

The full sparse (BGE-M3 learned-lexical) **Go chain is merged** (store inverted
index + scorer, embed seam + isolated onnx reader, engine RRF arm + toggle, serve
pill, `cmd/embed --sparse-only`, overlay carry, `validate --require-sparse`). What
remains is **data**, not code: the shipped model is dense-only, so `clause_sparse`
is empty and the arm stays off. Activation = export a sparse-headed model, re-embed
on GPU, fold it in, re-bake. Do these **supervised** (GPU hours; the re-embed spans
multiple Kaggle sessions).

Throughput reality: sparse comes from the SAME forward pass as dense (one extra
`Linear+ReLU`), so cost ≈ the dense embed — **GPU only** (CPU ≈ weeks for 2.85 M
clauses). Use Kaggle, like the dense pipeline.

## Step 1 — Export the model with the sparse head (one-off, ~30–60 min GPU)

Kaggle kernel `scripts/kaggle/kernel-export-sparse.py` (GPU + internet). Push + run:

```bash
export KAGGLE_API_TOKEN=$(cat ~/.kaggle/access_token)
mkdir -p /tmp/spk && cp scripts/kaggle/kernel-export-sparse.py /tmp/spk/kernel.py
cat > /tmp/spk/kernel-metadata.json <<'JSON'
{ "id":"makingcodes/3gpp-bge-m3-sparse-export","title":"3gpp bge-m3 sparse export",
  "code_file":"kernel.py","language":"python","kernel_type":"script",
  "is_private":true,"enable_gpu":true,"enable_internet":true }
JSON
kaggle kernels push -p /tmp/spk
kaggle kernels status makingcodes/3gpp-bge-m3-sparse-export   # until COMPLETE
kaggle kernels output makingcodes/3gpp-bge-m3-sparse-export -p /tmp/spk-out
grep -E "RESULT|MATCH_OK|DONE" /tmp/spk-out/*.log
```

GREEN means: `onnx_outputs=['sentence_embedding','sparse_weights']`, `MATCH_OK=True`
(ONNX sparse == FlagEmbedding `lexical_weights` within tol). The model is
`/kaggle/working/model.onnx[ + .onnx_data]`.

**Promote it to a Kaggle Dataset** the embed kernel can mount, e.g.
`makingcodes/bge-m3-sparse-onnx` (model.onnx + model.onnx_data + tokenizer.json).

## Step 2 — Register the model (Go side)

Add a `models.yaml` entry for the sparse model dir with **`sparse_output: sparse_weights`**
and make it active (or select via `EMBED_MODEL`). With that set, the onnx backend's
`EmbedSparse` builds its isolated session; without it the arm stays off. Keep the
dense `output: sentence_embedding` too (same model serves both).

## Step 3 — Populate `clause_sparse` on GPU (resumable, multi-session)

Canonical path (no merge/overlay changes): run on the **fused DB**:

```bash
mcp-3gpp ... # build with -tags onnx + the sparse model present
<binary> embed --db 3gpp.duckdb --sparse-only --sparse-batch 256   # repeat until 0 populated
```

`--sparse-only` re-queries "clauses with no sparse posting", so it is resumable
across the Kaggle 12 h cap — dispatch repeatedly until a run reports `0 populated`.
(A per-shard variant works too: embed sparse per series shard, then
`cmd/overlay` carries `clause_sparse` onto the base by natural identity. NOTE: the
per-lot `cmd/merge` path does NOT yet offset `clause_sparse.chunk_id` — prefer the
fused-DB path until that lands.)

Mirror the dense Kaggle automation (`corpus-embed-kaggle.yml` + `kernel-embed.py`):
the simplest is a sparse-mode flag on that kernel that swaps the model dataset and
runs `--sparse-only`. Until that exists, run Step 3 against a fused DB on a GPU box.

## Step 4 — Re-bake + verify

The data bake's recipe-hash guard will trigger a rebake (the recipe changes when
the sparse model/registry lands). Validate with `cmd/validate --require-sparse` and
confirm on `/dashboard.json`:

```bash
curl -s 'https://<host>/dashboard.json?token=…' | jq '{sparse_enabled,sparse_on}'
# expect sparse_enabled:true ; the dashboard "Sparse" pill turns on
```

Queries then fuse **BM25 + dense + sparse** via RRF; toggle live with
`POST /dashboard/toggle?name=sparse`.

## Status (2026-06-14)

The export recipe was test-run on Kaggle GPU (kernel `3gpp-bge-m3-sparse-export`)
to validate it against real BGE-M3 before wiring it into the production pipeline —
see the night report / kernel output for the result.
