# Embed throughput — research, measurements, and the optimisation ceiling

Status: 2026-06-03. Branch `feat/embed-pipeline-rebuild`. Hardware under test:
Kaggle 2×T4 (the engine uses one device — see the 2×T4 lock below). Model: BGE-M3
ONNX, fp16 (converted in-kernel, `keep_io_types`), 1024-dim dense.

This document captures WHAT was researched, WHAT was measured, and WHY the engine is
at its practical ceiling for the current stack — so the next person does not re-run
the same experiments.

## TL;DR

| lever | result | shipped default |
|---|---|---|
| HF Rust tokenizer (daulet, CGO) | **12× (4→48-60 cl/s)** — the whole win | ON (`fasttok` build) |
| fp16 weights | ~2-3× on T4 Tensor Cores (vs fp32) | ON |
| fixed-shape ladder | no effect / slight regression | OFF (opt-in) |
| token-budget (size-adaptive) batching | slight regression on this HW | OFF (opt-in) |
| CUDA shape warmup | raised util 30→37% but cut avg throughput | OFF (opt-in) |
| `EMBED_SESSIONS_PER_GPU=2` | no help (40.7 < 43.4 cl/s) | OFF |

**The bottleneck was CPU tokenisation, not the GPU.** Once the fast tokenizer fed the
GPU, the run became GPU-bound at **~30 % SM utilisation** — limited by H2D/D2H copy +
kernel-launch overhead through the `onnxruntime_go` binding (no device-tensor
IO-binding), which batch shaping cannot move. Further headroom needs a different
binding or execution provider (TensorRT), an architectural decision.

## Best-practice research (web)

- **ONNX Runtime + dynamic input shapes**: models with dynamic axes block some graph
  optimisations and force per-shape replanning; fixed shapes give a measurable
  throughput bump. → motivated the fixed-shape ladder.
  (onnxruntime.ai transformers-optimization; microsoft/onnxruntime issues #9799/#20983)
- **BGE-M3 ONNX**: fp16 + flash-attention + O2 graph opt → ~2-3× on GPU; full-length
  runs suggested batch ≤ 8 for the 567M model. (huggingface BAAI/bge-m3 discussions;
  aapot/philipchung bge-m3-onnx)
- **CUDA Graph + IOBinding**: CUDA Graphs need IOBinding + FIXED tensor addresses and
  help "static execution plans run more than twice"; avoid if shapes change often.
  Pinned memory enables async H2D/D2H. → device-resident IO is the real lever, but the
  Go binding exposes only CPU `OrtMemoryInfo`, so CUDA-graph/device-tensor is
  unreachable here. (onnxruntime.ai device-tensor; CUDA EP docs)
- **Go tokenizers**: `daulet/tokenizers` wraps the HuggingFace Rust tokenizers via CGO;
  CGO overhead 2-9 %, ~10 µs/encode (~95k clause/s) vs pure-Go `sugarme` ~17/s/thread.
  → the decisive lever. (github daulet/tokenizers; benchmarks)

## Systematic analysis — "maximum potential per clause size"

- **Clause length distribution** (series 21 slice, 4591 clauses): median ~147 tok, 79 %
  < 512 tok; only 13 clauses > 8k tok. So the corpus is short-clause dominated; long
  clauses are rare and capped at `maxTokens=512`.
- **Inference is tokens/s-bound, not clauses/s-bound.** Local CPU micro-bench (real
  BGE-M3): ~350-388 tok/s FLAT across seq lengths 128/256/512, and cold ≈ warm on the
  CPU EP (no replanning). So `clauses/s = tok_per_s ÷ padded_tokens_per_clause`: the
  levers that matter are (a) tighter padding and (b) a higher tok/s ceiling.
- **Padding is already tight**: `apply.go` length-sorts each 4096-window before
  batching, so a batch's rows are near-uniform length. Confirmed: T4 steady 8.4 cl/s ×
  188 tok/clause ≈ 1580 tok/s — the arithmetic closes.
- **Implication**: size-adaptive batching can only help by raising GPU *occupancy*
  (more rows per Run for short clauses) or cutting per-shape replanning. Both were
  built and measured (below) — neither helps on T4 + this binding, because the GPU is
  not occupancy- or replanning-bound; it is transfer/launch-bound at ~30 % util.

## Size-adaptive batching + pre-filter (built, measured, defaulted OFF)

Implemented in `internal/embed/embed_onnx.go` (all opt-in, identity-safe — extra
padding is attention-masked so vectors are byte-identical; the A2/bucketing/pipeline
gates pass):

- **Pre-filter / bucketing**: `apply.go` buffers a window and length-sorts it
  (`EMBED_BUCKET_WINDOW`), grouping similar-length clauses so each batch pads tight.
- **Fixed-shape ladder** (`EMBED_SHAPE_LADDER=1`): snap each batch's padded length up
  to a small rung set {16,32,64,96,128,192,256,384,512} → the session sees ~9 shapes,
  not one per length → plans cached.
- **Token-budget batching** (`EMBED_TOKEN_BUDGET_BATCHING=1`): size each batch to a
  constant padded-token budget instead of a constant row count → short clauses pack
  into big batches (occupancy), long clauses fall back to small ones (bounded VRAM).
  This is literally "batch algorithm as a function of clause size".
- **Shape warmup** (`EMBED_WARMUP=1`): pre-build the per-rung CUDA plans at init.

### Why they default OFF — the 2×T4 A/B (series 21, identical slice)

| config | cl/s avg | cl/s steady | GPU util | bound |
|---|---|---|---|---|
| sugarme baseline | 3.97 | 4.2 | 4 % | TOKENIZE |
| **daulet, levers OFF** | **48.35** | **59.7** | 30 % | RUN (GPU) |
| daulet + ladder + token-budget | 44.28 | 53.4 | 32 % | RUN |
| daulet + …+ warmup | 31.51 | 53.1 | 37 % | RUN |

The size-adaptive levers REGRESS once the run is GPU-bound: the byte→token estimate
oversizes batches for short-clause series, and warmup's init cost outweighs its small
util gain. The GPU sits at ~30 % util RUN-bound — the ceiling is memory-transfer/
launch overhead through the binding, which bigger/fixed-shape batches do not move. So
the validated-fastest path is plain fixed-`batchSize` daulet; the levers stay as
opt-in tunables (they may help a long-clause corpus or a binding with device tensors).

## The ceiling and the remaining levers (not yet done)

GPU RUN-bound at ~30 % util ⇒ the limiter is per-Run H2D/D2H + launch overhead. Within
`onnxruntime_go` (CPU `OrtMemoryInfo` only) the unexplored levers are:

1. **IOBinding with reused CPU tensors** (`CreateIoBinding`/`RunWithBinding`): avoids
   per-Run output allocation; may cut a slice of the overhead. NOT yet implemented.
2. **`do_copy_in_default_stream=0`**: allow copy/compute overlap (currently `1`).
   A/B not yet run.
3. **TensorRT EP / a binding with device tensors + CUDA-graph**: the real lever for
   >30 % util, but it needs the TRT provider lib and binding support — an
   architectural change (out of scope for this branch).

## Locks / constraints

- **2×T4 lock**: mono-GPU saturation only; NO in-process multi-device (the binding
  throws `CUDA failure 400` with >1 CUDA session — verified). The 2nd T4 is unused by
  design; cross-series 2-process is the only safe multi-GPU path and is not enabled.
- **Serve coherence (open)**: `cmd/server -tags onnx` tokenises QUERIES; a daulet-
  embedded corpus should be served by a daulet (`fasttok`) binary, or the tokenizer
  implementation should fold into `EmbedIdentity`, so query/corpus tokens cannot drift.

## Reproduce

- Tokenizer throughput: `go test ./internal/embed -run xxx -bench BenchmarkTokenizeThroughput`
- Inference micro-bench: `EMBED_MICROBENCH=1 go test ./internal/embed -tags onnx -run TestInferProfile`
- 2×T4 A/B: `scripts/kaggle/kernel-embed-probe.py` (sugarme vs daulet, profiled, util-sampled)
- Per-batch profile in any run: `EMBED_PROFILE=1` (logs tokenize-vs-Run split + tok/s)
