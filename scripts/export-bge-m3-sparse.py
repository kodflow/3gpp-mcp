#!/usr/bin/env python3
"""Export BAAI/bge-m3 to ONNX WITH the sparse (learned-lexical) head.

This is the one model-conversion prerequisite that lights up the production sparse
arm. It is NOT part of the query server (CLAUDE.md §13: Go-only at query time) — it
is an offline, run-once tool, alongside the Kaggle embed kernels, that produces a
model.onnx exposing BOTH outputs:

    inputs : input_ids, attention_mask
    outputs: sentence_embedding   [batch, 1024]  f32  (dense, CLS-pooled + L2)
             sparse_weights       [batch, seq]   f32  (ReLU(sparse_linear(h)) per token)

The Go side (internal/embed) reads sparse_weights and de-dups per token id (max,
specials dropped) — see toSparse / embed_onnx_sparse.go. After exporting:

  1. place model.onnx next to tokenizer.json under the model dir,
  2. add a registry entry (internal/embed/models.yaml) with `sparse_output: sparse_weights`
     and make it active, so EmbedSparse builds its session,
  3. populate the corpus: `mcp-3gpp`/cmd-embed `--sparse-only` (Kaggle GPU),
  4. re-bake the data image and redeploy.

Recipe sourced from FlagEmbedding (m3.py _process_token_weights; sparse_linear =
Linear(1024,1) + ReLU; no normalization) and the BGE-M3 paper (arXiv:2402.03216).

Usage:
    python scripts/export-bge-m3-sparse.py --out data/models/bge-m3-sparse/model.onnx
Requires: torch, transformers, FlagEmbedding (CPU is fine; export in fp32).
"""

import argparse

import torch
import torch.nn as nn
import torch.nn.functional as F
from FlagEmbedding import BGEM3FlagModel


class BGEM3DenseSparse(nn.Module):
    """Dense (CLS + L2) + compact per-position sparse weights = ReLU(sparse_linear(h))."""

    def __init__(self, m3: BGEM3FlagModel):
        super().__init__()
        inner = m3.model  # EncoderOnlyEmbedderM3ModelForInference
        self.backbone = inner.model  # XLM-RoBERTa encoder
        self.sparse_linear = inner.sparse_linear  # Linear(1024, 1), weights loaded

    def forward(self, input_ids, attention_mask):
        h = self.backbone(
            input_ids=input_ids, attention_mask=attention_mask, return_dict=True
        ).last_hidden_state  # [B, S, 1024]
        # Dense head: CLS pooling + L2 (BGE-M3's documented pooling).
        sentence_embedding = F.normalize(h[:, 0], p=2, dim=-1)  # [B, 1024]
        # Sparse head: Linear(1024,1) -> ReLU -> squeeze; zero out padding. No norm.
        sparse = F.relu(self.sparse_linear(h)).squeeze(-1)  # [B, S]
        sparse = sparse * attention_mask.to(sparse.dtype)
        return sentence_embedding, sparse


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", default="BAAI/bge-m3")
    ap.add_argument("--out", required=True, help="output model.onnx path")
    ap.add_argument("--opset", type=int, default=17)
    args = ap.parse_args()

    m3 = BGEM3FlagModel(args.model, use_fp16=False)  # export fp32 (sparse precision)
    wrapper = BGEM3DenseSparse(m3).eval()

    enc = m3.tokenizer(
        ["AMF SMF N4 PFCP registration procedure"],
        return_tensors="pt",
        padding=True,
        truncation=True,
        max_length=8192,
    )
    torch.onnx.export(
        wrapper,
        (enc["input_ids"], enc["attention_mask"]),
        args.out,
        input_names=["input_ids", "attention_mask"],
        output_names=["sentence_embedding", "sparse_weights"],
        dynamic_axes={
            "input_ids": {0: "batch", 1: "seq"},
            "attention_mask": {0: "batch", 1: "seq"},
            "sentence_embedding": {0: "batch"},
            "sparse_weights": {0: "batch", 1: "seq"},
        },
        opset_version=args.opset,
        export_params=True,
        do_constant_folding=True,
    )
    print(f"exported {args.out} (opset {args.opset}) with outputs "
          "[sentence_embedding, sparse_weights]")

    # Sanity: ONNX sparse_weights (after Go-side dedup) should match FlagEmbedding's
    # lexical_weights. Verify on acronym-heavy strings before trusting it (brief §e).
    ref = m3.encode(["AMF SMF N4 PFCP registration procedure"], return_sparse=True)
    print("FlagEmbedding lexical_weights (reference, dedup):",
          ref["lexical_weights"][0])


if __name__ == "__main__":
    main()
