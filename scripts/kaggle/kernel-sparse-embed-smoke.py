# Sparse EMBED end-to-end SMOKE on real GPU/ORT (one-off, GPU+internet).
# Proves the last unproven link: the onnx EmbedSparse session (embed_onnx_sparse.go)
# running on real ONNX Runtime + `cmd/embed --sparse-only` populating clause_sparse.
# Self-contained: exports the dense+sparse model inline, builds the onnx embed binary,
# seeds a tiny DB via scripts/smoke-seed, runs --sparse-only, asserts populated.
import os
import re
import subprocess
import sys
import glob

WORK = "/kaggle/working"
REPO = "https://github.com/kodflow/3gpp-mcp"
BRANCH = "main"
ORT_VERSION = "1.26.0"


def sh(c, env=None):
    print("+", c, flush=True)
    return subprocess.run(c, shell=True, text=True, capture_output=True, env=env or os.environ.copy())


def res(m):
    print("RESULT " + m, flush=True)
    try:
        open(WORK + "/RESULT.txt", "a").write("RESULT " + m + "\n")
    except Exception:
        pass


try:
    # --- 1. Export the dense+sparse model (validated recipe) ------------------
    # No `| tail`: piping the install through tail masked a failed pip step. The
    # import right after surfaces a broken install, but keep pip's own errors visible.
    pi = sh("pip -q install FlagEmbedding onnx onnxruntime onnxscript")
    if pi.returncode != 0:
        res("fail pip_install " + (pi.stderr or pi.stdout or "")[-300:])
        sys.exit(1)
    import torch
    import torch.nn as nn
    import torch.nn.functional as F
    from FlagEmbedding import BGEM3FlagModel

    m3 = BGEM3FlagModel("BAAI/bge-m3", use_fp16=False)
    inner = m3.model
    backbone, sparse_linear = inner.model, inner.sparse_linear
    MDIR = WORK + "/model"
    os.makedirs(MDIR, exist_ok=True)

    class DenseSparse(nn.Module):
        def __init__(self, b, s):
            super().__init__()
            self.backbone, self.sparse_linear = b, s

        def forward(self, input_ids, attention_mask):
            h = self.backbone(input_ids=input_ids, attention_mask=attention_mask, return_dict=True).last_hidden_state
            dense = F.normalize(h[:, 0], p=2, dim=-1)
            sparse = F.relu(self.sparse_linear(h)).squeeze(-1) * attention_mask.to(h.dtype)
            return dense, sparse

    enc = m3.tokenizer(["AMF SMF N4 registration"], return_tensors="pt", padding=True, truncation=True, max_length=8192)
    torch.onnx.export(
        DenseSparse(backbone, sparse_linear).eval(),
        (enc["input_ids"], enc["attention_mask"]), MDIR + "/model.onnx",
        input_names=["input_ids", "attention_mask"],
        output_names=["sentence_embedding", "sparse_weights"],
        dynamic_axes={"input_ids": {0: "batch", 1: "seq"}, "attention_mask": {0: "batch", 1: "seq"},
                      "sentence_embedding": {0: "batch"}, "sparse_weights": {0: "batch", 1: "seq"}},
        opset_version=17, export_params=True, do_constant_folding=True)
    m3.tokenizer.save_pretrained(MDIR)
    res("model_exported has_data=%s has_tok=%s" % (
        os.path.exists(MDIR + "/model.onnx_data"), os.path.exists(MDIR + "/tokenizer.json")))

    # --- 2. Toolchain: Go (from go.mod), ORT-gpu lib -------------------------
    gomod = sh('curl -fsSL --retry 5 "%s/raw/%s/go.mod"' % (REPO, BRANCH)).stdout
    gov = re.search(r'^go (\d+\.\d+(?:\.\d+)?)', gomod, re.M).group(1)
    sh("curl -fsSL --retry 5 https://go.dev/dl/go%s.linux-amd64.tar.gz -o /tmp/go.tgz" % gov)
    sh("rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz")
    os.environ["PATH"] = "/usr/local/go/bin:" + os.environ["PATH"]
    os.environ["GOTOOLCHAIN"] = "local"
    res("go=" + sh("go version").stdout.strip())
    sh("curl -fsSL --retry 5 -o /tmp/ort.tgz "
       "https://github.com/microsoft/onnxruntime/releases/download/v%s/onnxruntime-linux-x64-gpu-%s.tgz"
       % (ORT_VERSION, ORT_VERSION))
    sh("mkdir -p %s/ort && tar -C %s/ort --strip-components=1 -xzf /tmp/ort.tgz" % (WORK, WORK))
    libs = glob.glob("%s/ort/**/libonnxruntime.so*" % WORK, recursive=True)
    ORT_LIB = sorted(libs, key=len)[0]
    ORT_DIR = os.path.dirname(ORT_LIB)
    res("ort_lib=" + os.path.basename(ORT_LIB))

    # --- 3. Clone repo (main) + build seeder + onnx embed binary -------------
    src = WORK + "/src"
    if sh('git clone --depth 1 -b %s %s %s' % (BRANCH, REPO, src)).returncode != 0:
        res("fail clone"); sys.exit(1)
    benv = os.environ.copy()
    benv["CGO_ENABLED"] = "1"
    benv["ONNXRUNTIME_SHARED_LIBRARY_PATH"] = ORT_LIB
    benv["LD_LIBRARY_PATH"] = ORT_DIR + ":" + benv.get("LD_LIBRARY_PATH", "")
    b1 = sh('cd %s && go build -o %s/seed ./scripts/smoke-seed' % (src, WORK), env=benv)
    if b1.returncode != 0:
        res("fail build_seed " + (b1.stderr or "")[-300:]); sys.exit(1)
    b2 = sh('cd %s && go build -tags onnx -o %s/embed ./cmd/embed' % (src, WORK), env=benv)
    if b2.returncode != 0:
        res("fail build_embed " + (b2.stderr or "")[-400:]); sys.exit(1)
    res("built seed+embed (onnx)")

    # --- 4. models.yaml with sparse_output ----------------------------------
    with open(WORK + "/models.yaml", "w") as mf:
        mf.write(
            "active: bge-m3-sparse\nmodels:\n"
            "  - name: bge-m3-sparse\n    family: bge-m3\n    dir: %s\n"
            "    precision: fp32\n    dim: 1024\n    normalization: l2\n"
            "    revision: smoke\n    tokenizer_revision: smoke\n    tokenizer_dir: %s\n"
            "    inputs: [input_ids, attention_mask]\n    output: sentence_embedding\n"
            "    sparse_output: sparse_weights\n" % (MDIR, MDIR))

    # --- 5. Seed a tiny DB + run --sparse-only on GPU -----------------------
    DB = WORK + "/smoke.duckdb"
    s = sh('%s/seed %s' % (WORK, DB), env=benv)
    res("seed: " + (s.stdout or s.stderr or "").strip()[-80:])
    eenv = benv.copy()
    eenv["EMBED_MODELS_CONFIG"] = WORK + "/models.yaml"
    eenv["EMBED_MODEL"] = "bge-m3-sparse"
    eenv["ORT_EP"] = "cuda"  # GPU; degrades to CPU if the EP can't init
    r = sh('%s/embed --db %s --sparse-only --sparse-batch 64' % (WORK, DB), env=eenv)
    print("EMBED STDOUT:", (r.stdout or "")[-500:], flush=True)
    print("EMBED STDERR:", (r.stderr or "")[-500:], flush=True)
    m = re.search(r'sparse embed done: (\d+) clause', r.stdout or "")
    n = int(m.group(1)) if m else -1
    res("sparse_only_populated=%d rc=%d" % (n, r.returncode))

    # --- 6. Confirm clause_sparse rows exist (count via the embed binary's DB) -
    # Re-run --sparse-only: a converged DB reports 0 new (idempotent/resumable proof).
    r2 = sh('%s/embed --db %s --sparse-only' % (WORK, DB), env=eenv)
    m2 = re.search(r'sparse embed done: (\d+) clause', r2.stdout or "")
    n2 = int(m2.group(1)) if m2 else -1
    ok = (n == 6 and n2 == 0)
    res("idempotent_second_pass=%d" % n2)
    res("DONE sparse_embed_gpu_ok=%s (first=%d second=%d rc=%d)" % (ok, n, n2, r.returncode))
    sys.exit(0 if ok else 1)
except Exception as e:
    import traceback
    traceback.print_exc()
    res("fail exception=%s %s" % (type(e).__name__, str(e)[:200]))
    sys.exit(1)
