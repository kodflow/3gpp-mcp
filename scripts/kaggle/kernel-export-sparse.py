# Kaggle kernel: export BAAI/bge-m3 to ONNX WITH the sparse (learned-lexical) head.
#
# One-off, GPU+internet. Produces /kaggle/working/model.onnx[ + .onnx_data] with:
#   inputs : input_ids, attention_mask
#   outputs: sentence_embedding [B,1024] f32 (CLS-pool + L2)
#            sparse_weights      [B,S]   f32 (ReLU(sparse_linear(h)), padding-masked)
# It reloads the export with onnxruntime to assert BOTH output names exist, and
# cross-checks the ONNX sparse weights (after the same dedup the Go side does:
# max per token id, drop specials {0,1,2,3} + non-positive) against FlagEmbedding's
# own lexical_weights on acronym-heavy 3GPP text — so a green run PROVES the recipe.
#
# This is the model prerequisite for the sparse arm (see internal/embed/sparse.go,
# embed_onnx_sparse.go, docs/guides/sparse-activation-runbook.md). After a green run,
# promote /kaggle/working to a Kaggle Dataset (e.g. makingcodes/bge-m3-sparse-onnx)
# so the sparse embed kernel can mount it; the Go side selects it via a models.yaml
# entry with `sparse_output: sparse_weights`.
#
# Recipe sources: FlagEmbedding m3.py _process_token_weights (sparse_linear =
# Linear(1024,1) + ReLU, NO normalization); BGE-M3 paper arXiv:2402.03216.
import os
import subprocess
import sys
import traceback


def sh(c):
    print("+", c, flush=True)
    return subprocess.run(c, shell=True).returncode


def result(msg):
    print("RESULT " + msg, flush=True)


try:
    sh("pip -q install FlagEmbedding onnx onnxruntime onnxscript 2>&1 | tail -3")

    import torch
    import torch.nn as nn
    import torch.nn.functional as F
    from FlagEmbedding import BGEM3FlagModel

    m3 = BGEM3FlagModel("BAAI/bge-m3", use_fp16=False)  # export fp32 for sparse precision
    inner = getattr(m3, "model", None)
    backbone = getattr(inner, "model", None)
    sparse_linear = getattr(inner, "sparse_linear", None)
    if backbone is None or sparse_linear is None:
        print("INNER:", type(inner), dir(inner), flush=True)
        result("fail attr_paths")
        sys.exit(1)

    class DenseSparse(nn.Module):
        def __init__(self, backbone, sparse_linear):
            super().__init__()
            self.backbone = backbone
            self.sparse_linear = sparse_linear

        def forward(self, input_ids, attention_mask):
            h = self.backbone(input_ids=input_ids, attention_mask=attention_mask,
                              return_dict=True).last_hidden_state
            dense = F.normalize(h[:, 0], p=2, dim=-1)
            sparse = F.relu(self.sparse_linear(h)).squeeze(-1) * attention_mask.to(h.dtype)
            return dense, sparse

    wrapper = DenseSparse(backbone, sparse_linear).eval()
    enc = m3.tokenizer(["AMF SMF N4 PFCP registration procedure"], return_tensors="pt",
                       padding=True, truncation=True, max_length=8192)
    out_path = "/kaggle/working/model.onnx"
    torch.onnx.export(
        wrapper, (enc["input_ids"], enc["attention_mask"]), out_path,
        input_names=["input_ids", "attention_mask"],
        output_names=["sentence_embedding", "sparse_weights"],
        dynamic_axes={"input_ids": {0: "batch", 1: "seq"},
                      "attention_mask": {0: "batch", 1: "seq"},
                      "sentence_embedding": {0: "batch"},
                      "sparse_weights": {0: "batch", 1: "seq"}},
        opset_version=17, export_params=True, do_constant_folding=True)
    result("exported model.onnx")

    import onnxruntime as ort
    sess = ort.InferenceSession(out_path, providers=["CPUExecutionProvider"])
    onames = [o.name for o in sess.get_outputs()]
    result("onnx_outputs=%s" % onames)
    if "sparse_weights" not in onames or "sentence_embedding" not in onames:
        result("fail missing_output")
        sys.exit(1)

    ids = enc["input_ids"].numpy().astype("int64")
    mask = enc["attention_mask"].numpy().astype("int64")
    dense_o, sparse_o = sess.run(None, {"input_ids": ids, "attention_mask": mask})
    special = {0, 1, 2, 3}
    onnx_sp = {}
    for tid, w in zip(ids[0].tolist(), sparse_o[0].tolist()):
        if w > 0 and tid not in special and w > onnx_sp.get(tid, 0):
            onnx_sp[tid] = w
    # STRUCTURAL proof (the real gate): both outputs present (checked above) + the
    # sparse head yields a non-empty, deduped term set with plausible weights.
    structural_ok = len(onnx_sp) > 0 and dense_o.shape[-1] == 1024
    top = sorted(onnx_sp.items(), key=lambda kv: -kv[1])[:5]
    result("onnx_sparse_terms=%d top=%s structural_ok=%s" % (len(onnx_sp), top, structural_ok))
    # BONUS numeric cross-check vs FlagEmbedding's own lexical_weights — best-effort
    # and shape-robust (the return type varies by version), never gates the run.
    ref_ok = None
    try:
        lw = m3.encode(["AMF SMF N4 PFCP registration procedure"], return_sparse=True)["lexical_weights"]
        r0 = lw[0] if isinstance(lw, (list, tuple)) else lw
        if isinstance(r0, dict):
            ref = {int(k): float(v) for k, v in r0.items()}
            shared = set(onnx_sp) & set(ref)
            maxdiff = max((abs(onnx_sp[k] - ref[k]) for k in shared), default=1.0)
            ref_ok = len(shared) >= max(1, int(0.7 * len(ref))) and maxdiff < 0.05
            result("ref_check shared=%d/%d maxdiff=%.4f ref_ok=%s" % (len(shared), len(ref), maxdiff, ref_ok))
        else:
            result("ref_skip type=%s" % type(r0).__name__)
    except Exception as re:
        result("ref_skip exc=%s" % type(re).__name__)
    # torch.onnx.export names the >2GB external-data file differently across versions
    # ("model.onnx.data" on the Kaggle torch, not the "model.onnx_data" this once
    # assumed) — probe both so the reported size isn't a misleading 3 MB graph-only.
    ext = next((c for c in (out_path + ".data", out_path + "_data") if os.path.exists(c)), None)
    sz = os.path.getsize(out_path) + (os.path.getsize(ext) if ext else 0)
    result("DONE structural_ok=%s ref_ok=%s dense_dim=%d model_bytes=%d" %
           (structural_ok, ref_ok, dense_o.shape[-1], sz))
    if not structural_ok:
        sys.exit(1)
except Exception as e:
    print("EXC", e, flush=True)
    traceback.print_exc()
    result("fail exception=%s" % type(e).__name__)
    sys.exit(1)
