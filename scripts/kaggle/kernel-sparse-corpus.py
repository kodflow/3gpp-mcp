# Corpus-scale SPARSE pass on Kaggle GPU (additive — never recomputes dense).
# Pulls the private lexical base (ghcr.io/<owner>/3gpp-corpus:latest) via crane,
# exports the dense+sparse BGE-M3 model (proven recipe, kernel-sparse-embed-smoke.py),
# builds `cmd/embed -tags onnx` on ORT-CUDA, runs `--sparse-only` over the corpus
# (resumable by construction: it only fills clauses missing clause_sparse), then
# emits the produced sparse identity + a compact clause_sparse dump for the workflow
# to publish/fold. The dense `embedding` column is NEVER touched.
#
# Env (injected by corpus-sparse-kaggle.yml, never written into the kernel source):
#   GHCR_OWNER  GHCR org (default kodflow)
#   GHCR_PAT    read:packages token for the private base pull (STDIN only)
#   BRANCH      repo branch to build from (default main)
#   EMBED_FLOOR release floor (informational; sparse covers all clauses)
import glob
import os
import re
import subprocess
import sys

WORK = "/kaggle/working"
REPO = "https://github.com/kodflow/3gpp-mcp"
BRANCH = os.environ.get("BRANCH", "main")
OWNER = os.environ.get("GHCR_OWNER", "kodflow")
GHCR_PAT = os.environ.get("GHCR_PAT", "").strip()
ORT_VERSION = "1.26.0"
CRANE_VER = "v0.20.2"
DB = WORK + "/3gpp.duckdb"
MDIR = WORK + "/model"

if not re.match(r"^[A-Za-z0-9._-]+$", OWNER):
    print("RESULT fail bad_owner=%s" % OWNER, flush=True)
    sys.exit(0)


def sh(c, env=None, check=False):
    print("+", c, flush=True)
    r = subprocess.run(c, shell=True, text=True, capture_output=True, env=env or os.environ.copy())
    if check and r.returncode != 0:
        print("STDERR:", (r.stderr or "")[-600:], flush=True)
        raise RuntimeError("command failed rc=%d: %s" % (r.returncode, c))
    return r


def res(m):
    print("RESULT " + m, flush=True)
    try:
        open(WORK + "/RESULT.txt", "a").write("RESULT " + m + "\n")
    except Exception:
        pass


try:
    if not GHCR_PAT:
        res("fail ghcr_pat_missing")
        sys.exit(0)

    # --- 1. Export the dense+sparse model (validated recipe) ------------------
    pi = sh("pip -q install FlagEmbedding onnx onnxruntime onnxscript")
    if pi.returncode != 0:
        res("fail pip_install " + (pi.stderr or pi.stdout or "")[-300:])
        sys.exit(0)
    import torch
    import torch.nn as nn
    import torch.nn.functional as F
    from FlagEmbedding import BGEM3FlagModel

    m3 = BGEM3FlagModel("BAAI/bge-m3", use_fp16=False)
    inner = m3.model
    backbone, sparse_linear = inner.model, inner.sparse_linear
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
    res("model_exported has_data=%s" % os.path.exists(MDIR + "/model.onnx_data"))

    # --- 2. Toolchain: Go (from go.mod), crane, ORT-gpu ----------------------
    gomod = sh('curl -fsSL --retry 5 "%s/raw/%s/go.mod"' % (REPO, BRANCH)).stdout
    gov = re.search(r'^go (\d+\.\d+(?:\.\d+)?)', gomod, re.M).group(1)
    sh("curl -fsSL --retry 5 https://go.dev/dl/go%s.linux-amd64.tar.gz -o /tmp/go.tgz" % gov, check=True)
    sh("rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz", check=True)
    os.environ["PATH"] = "/usr/local/go/bin:" + os.environ["PATH"]
    os.environ["GOTOOLCHAIN"] = "local"
    res("go=" + sh("go version").stdout.strip())
    sh("curl -fsSL --retry 5 -o /tmp/ort.tgz "
       "https://github.com/microsoft/onnxruntime/releases/download/v%s/onnxruntime-linux-x64-gpu-%s.tgz"
       % (ORT_VERSION, ORT_VERSION), check=True)
    sh("mkdir -p %s/ort && tar -C %s/ort --strip-components=1 -xzf /tmp/ort.tgz" % (WORK, WORK), check=True)
    libs = glob.glob("%s/ort/**/libonnxruntime.so*" % WORK, recursive=True)
    ORT_LIB = sorted(libs, key=len)[0]
    ORT_DIR = os.path.dirname(ORT_LIB)

    # --- 3. Pull the private lexical base via crane (PAT on STDIN only) -------
    sh('curl -fsSL --retry 5 -o /tmp/crane.tgz '
       '"https://github.com/google/go-containerregistry/releases/download/%s/'
       'go-containerregistry_Linux_x86_64.tar.gz"' % CRANE_VER, check=True)
    sh('tar -xzf /tmp/crane.tgz -C /tmp crane', check=True)
    if sh('printf %%s "$GHCR_PAT" | /tmp/crane auth login ghcr.io -u "%s" --password-stdin' % OWNER).returncode != 0:
        res("fail ghcr_login")
        sys.exit(0)
    img = "ghcr.io/%s/3gpp-corpus:latest" % OWNER
    if sh('/tmp/crane export "%s" - | tar -xC "%s" 3gpp.duckdb' % (img, WORK)).returncode != 0:
        res("fail ghcr_export " + img)
        sys.exit(0)
    if not os.path.isfile(DB):
        res("fail ghcr_no_db")
        sys.exit(0)

    # --- 4. Build the onnx embed binary + a sparse-active registry -----------
    src = WORK + "/src"
    if sh('git clone --depth 1 -b %s %s %s' % (BRANCH, REPO, src)).returncode != 0:
        res("fail clone")
        sys.exit(0)
    benv = os.environ.copy()
    benv["CGO_ENABLED"] = "1"
    benv["ONNXRUNTIME_SHARED_LIBRARY_PATH"] = ORT_LIB
    benv["LD_LIBRARY_PATH"] = ORT_DIR + ":" + benv.get("LD_LIBRARY_PATH", "")
    b = sh('cd %s && go build -tags onnx -o %s/embed ./cmd/embed' % (src, WORK), env=benv)
    if b.returncode != 0:
        res("fail build_embed " + (b.stderr or "")[-400:])
        sys.exit(0)
    with open(WORK + "/models.yaml", "w") as mf:
        mf.write(
            "active: bge-m3-sparse\nmodels:\n"
            "  - name: bge-m3-sparse\n    family: bge-m3\n    dir: %s\n"
            "    precision: fp32\n    dim: 1024\n    normalization: l2\n"
            "    revision: 5617a9f\n    tokenizer_revision: 5617a9f\n    tokenizer_dir: %s\n"
            "    inputs: [input_ids, attention_mask]\n    output: sentence_embedding\n"
            "    sparse_output: sparse_weights\n" % (MDIR, MDIR))

    # --- 5. Run --sparse-only over the corpus (additive; resumable) ----------
    eenv = benv.copy()
    eenv["EMBED_MODELS_CONFIG"] = WORK + "/models.yaml"
    eenv["EMBED_MODEL"] = "bge-m3-sparse"
    eenv["ORT_EP"] = "cuda"
    r = sh('%s/embed --db %s --sparse-only --sparse-batch 256' % (WORK, DB), env=eenv)
    print("EMBED STDOUT:", (r.stdout or "")[-600:], flush=True)
    print("EMBED STDERR:", (r.stderr or "")[-600:], flush=True)
    if r.returncode != 0:
        res("fail sparse_only rc=%d" % r.returncode)
        sys.exit(0)
    populated = sh('duckdb "%s" -noheader -list "SELECT count(*) FROM clause_sparse;"' % DB).stdout.strip()
    res("clause_sparse_rows=%s" % populated)

    # --- 6. Emit the sparse identity + a compact clause_sparse dump ----------
    sh('%s/embed --db %s --sparse-only' % (WORK, DB), env=eenv)  # 2nd pass: 0 new (converged) + re-stamps
    # The stamped sparse_model is the published identity; read it back for the index.
    sm = sh('duckdb "%s" -noheader -list "SELECT value FROM schema_meta WHERE key=\'sparse_model\';"' % DB).stdout.strip()
    with open(WORK + "/sparse-index.json", "w") as sf:
        sf.write('{"sparse_model":"%s"}\n' % sm)
    # Compact FOLDABLE shard: cmd/overlay overlaySparse re-keys clause_sparse onto the
    # base by NATURAL IDENTITY (spec_id, release, clause_path, text), so the shard must
    # carry clauses(chunk_id + identity cols + text) AND clause_sparse(chunk_id, ...).
    # We export ONLY clauses that have sparse rows + their postings — no embeddings, no
    # HNSW, no catalogue — so the artifact stays a fraction of the full DB.
    SHARD = WORK + "/sparse-shard.duckdb"
    shard_sql = (
        "ATTACH '%s' AS f (READ_ONLY); "
        "CREATE TABLE clauses AS SELECT chunk_id, spec_id, release, version, clause_path, text "
        "FROM f.clauses WHERE chunk_id IN (SELECT chunk_id FROM f.clause_sparse); "
        "CREATE TABLE clause_sparse AS SELECT chunk_id, term_id, weight FROM f.clause_sparse;"
    ) % DB
    if sh('duckdb "%s" "%s"' % (SHARD, shard_sql)).returncode != 0:
        res("fail shard_export")
        sys.exit(0)
    res("DONE sparse_model=%s rows=%s shard=%s" % (
        sm, populated, os.path.exists(SHARD)))
    sys.exit(0)
except Exception as e:
    import traceback
    traceback.print_exc()
    res("fail exception=%s %s" % (type(e).__name__, str(e)[:200]))
    sys.exit(0)
