# Corpus-scale SPARSE pass on Kaggle GPU (additive — never recomputes dense).
# Pulls the private lexical base (ghcr.io/<owner>/3gpp-corpus:latest) via crane,
# exports the dense+sparse BGE-M3 model (proven recipe, kernel-sparse-embed-smoke.py),
# builds `cmd/embed` + `cmd/overlay` (-tags onnx) on ORT-CUDA, then:
#   RESUME — pulls the previously published sparse layer (ghcr.io/<owner>/3gpp-sparse
#            :latest, if any) and overlays its clause_sparse onto the fresh base, so
#            this session only embeds clauses STILL missing a posting.
#   PASS   — runs `--sparse-only --limit EMBED_LIMIT` (a BOUNDED slice that fits under
#            Kaggle's 12 h cap; ClausesMissingSparse skips already-populated clauses).
#   EMIT   — exports the CUMULATIVE clause_sparse shard to /kaggle/working so the
#            workflow republishes it; the NEXT dispatch resumes from it.
# The dense `embedding` column is NEVER touched. A full corpus is covered across
# several bounded re-dispatches: each one saves its work, none restart from zero.
#
# Env (injected by corpus-sparse-kaggle.yml, never written into the kernel source):
#   GHCR_OWNER  GHCR org (default kodflow)
#   GHCR_PAT    read:packages token for the private base + sparse-channel pull (STDIN)
#   BRANCH      repo branch to build from (default main)
#   EMBED_FLOOR release floor (informational; sparse covers all clauses)
#   EMBED_LIMIT clauses to embed THIS session (bounded slice; 0 = whole remainder)
import glob
import os
import re
import subprocess
import sys

# /kaggle/working is COMMITTED as the kernel output (and re-downloaded by the
# workflow). Keep it TINY — only RESULT.txt + sparse-index.json + sparse-shard.duckdb
# live here. All heavy intermediates (the multi-GB pulled base DB, the ~2 GB model,
# ORT, the cloned src) go to /tmp, which Kaggle does NOT commit — otherwise the
# output download is multi-GB and breaks with IncompleteRead (the failure that hid
# every RESULT marker).
WORK = "/kaggle/working"
TMP = "/tmp/work"
REPO = "https://github.com/kodflow/3gpp-mcp"
BRANCH = os.environ.get("BRANCH", "main")
OWNER = os.environ.get("GHCR_OWNER", "kodflow")
GHCR_PAT = os.environ.get("GHCR_PAT", "").strip()
ORT_VERSION = "1.26.0"
CRANE_VER = "v0.20.2"
ORAS_VERSION = "1.2.0"  # pull the previously published sparse shard (resume input)
DUCKDB_CLI_VER = "v1.1.3"  # storage-compatible with go-duckdb here (see kernel-embed.py)
# EMBED_LIMIT>0 caps the sparse pass to N clauses — a fast end-to-end validation of
# the whole chain (kernel → shard → GHCR publish → bake) without the multi-hour full
# corpus run. 0 = full corpus.
LIMIT = os.environ.get("EMBED_LIMIT", "0").strip()
# Real 3GPP clauses run up to 8192 tokens, so the sparse head's [batch, seq] tensor
# is memory-hungry: batch 256 OOM'd a T4 (a single Add node wanted 3.5 GB). Memory is
# linear in batch, so a small batch keeps it well within 16 GB. Override via SPARSE_BATCH.
SPARSE_BATCH = os.environ.get("SPARSE_BATCH", "16").strip() or "16"
DB = TMP + "/3gpp.duckdb"
MDIR = TMP + "/model"
os.makedirs(TMP, exist_ok=True)

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
    # GPU presence marker (read by CI's kaggle-gpu-check.sh for the quota fallback):
    # a no-GPU session means this account's weekly GPU quota is exhausted, so the CI
    # falls back to the other Kaggle account. Mirrors the rust/embed kernels.
    _g = sh("nvidia-smi -L")
    if _g.returncode == 0 and _g.stdout.strip():
        res("gpu=present detail=%s" % _g.stdout.splitlines()[0].strip())
    else:
        res("gpu=absent (CPU fallback — GPU not attached to this worker)")

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
    sh("mkdir -p %s/ort && tar -C %s/ort --strip-components=1 -xzf /tmp/ort.tgz" % (TMP, TMP), check=True)
    libs = glob.glob("%s/ort/**/libonnxruntime.so*" % TMP, recursive=True)
    ORT_LIB = sorted(libs, key=len)[0]
    ORT_DIR = os.path.dirname(ORT_LIB)

    # duckdb CLI + oras + zstd: Kaggle ships the duckdb PYTHON package, not the shell
    # binary, so every `duckdb …` call (count / shard export) silently no-op'd before.
    # Install all three into /tmp/bin (PATH-prepended). oras pulls the previously
    # published sparse shard for RESUME; zstd decompresses it. duckdb v1.1.3 is the
    # version proven storage-compatible with this go-duckdb build (see kernel-embed.py).
    os.makedirs("/tmp/bin", exist_ok=True)
    sh("curl -fsSL --retry 5 -o /tmp/duckdb.zip "
       "https://github.com/duckdb/duckdb/releases/download/%s/duckdb_cli-linux-amd64.zip"
       % DUCKDB_CLI_VER, check=True)
    sh("cd /tmp/bin && unzip -o -q /tmp/duckdb.zip", check=True)
    sh("curl -fsSL --retry 5 -o /tmp/oras.tgz "
       "https://github.com/oras-project/oras/releases/download/v%s/oras_%s_linux_amd64.tar.gz"
       % (ORAS_VERSION, ORAS_VERSION), check=True)
    sh("tar -xzf /tmp/oras.tgz -C /tmp/bin oras", check=True)
    os.environ["PATH"] = "/tmp/bin:" + os.environ["PATH"]
    if sh("duckdb --version").returncode != 0:
        res("fail duckdb_cli")
        sys.exit(0)
    sh("command -v zstd >/dev/null 2>&1 || (apt-get update -y && apt-get install -y --no-install-recommends zstd)")

    # --- 3. Pull the private lexical base via crane (PAT on STDIN only) -------
    sh('curl -fsSL --retry 5 -o /tmp/crane.tgz '
       '"https://github.com/google/go-containerregistry/releases/download/%s/'
       'go-containerregistry_Linux_x86_64.tar.gz"' % CRANE_VER, check=True)
    sh('tar -xzf /tmp/crane.tgz -C /tmp crane', check=True)
    if sh('printf %%s "$GHCR_PAT" | /tmp/crane auth login ghcr.io -u "%s" --password-stdin' % OWNER).returncode != 0:
        res("fail ghcr_login")
        sys.exit(0)
    img = "ghcr.io/%s/3gpp-corpus:latest" % OWNER
    if sh('/tmp/crane export "%s" - | tar -xC "%s" 3gpp.duckdb' % (img, TMP)).returncode != 0:
        res("fail ghcr_export " + img)
        sys.exit(0)
    if not os.path.isfile(DB):
        res("fail ghcr_no_db")
        sys.exit(0)

    # --- 4. Build the onnx embed binary + a sparse-active registry -----------
    src = TMP + "/src"
    if sh('git clone --depth 1 -b %s %s %s' % (BRANCH, REPO, src)).returncode != 0:
        res("fail clone")
        sys.exit(0)
    benv = os.environ.copy()
    benv["CGO_ENABLED"] = "1"
    benv["ONNXRUNTIME_SHARED_LIBRARY_PATH"] = ORT_LIB
    benv["LD_LIBRARY_PATH"] = ORT_DIR + ":" + benv.get("LD_LIBRARY_PATH", "")
    b = sh('cd %s && go build -tags onnx -o %s/embed ./cmd/embed' % (src, TMP), env=benv)
    if b.returncode != 0:
        res("fail build_embed " + (b.stderr or "")[-400:])
        sys.exit(0)
    # cmd/overlay carries an attached shard's clause_sparse onto the base (overlaySparse)
    # — the RESUME carry-over. Built with the same onnx tag so the binary links the same
    # go-duckdb engine that wrote the base.
    bo = sh('cd %s && go build -tags onnx -o %s/overlay ./cmd/overlay' % (src, TMP), env=benv)
    if bo.returncode != 0:
        res("fail build_overlay " + (bo.stderr or "")[-400:])
        sys.exit(0)
    with open(TMP + "/models.yaml", "w") as mf:
        mf.write(
            "active: bge-m3-sparse\nmodels:\n"
            "  - name: bge-m3-sparse\n    family: bge-m3\n    dir: %s\n"
            "    precision: fp32\n    dim: 1024\n    normalization: l2\n"
            "    revision: 5617a9f\n    tokenizer_revision: 5617a9f\n    tokenizer_dir: %s\n"
            "    inputs: [input_ids, attention_mask]\n    output: sentence_embedding\n"
            "    sparse_output: sparse_weights\n" % (MDIR, MDIR))

    # --- 4.5 RESUME: overlay the previously published sparse layer onto the base --
    # The whole point of being resumable: pull the sparse shard the LAST session
    # published (ghcr.io/<owner>/3gpp-sparse:latest) and re-inject its clause_sparse
    # into THIS fresh lexical base via cmd/overlay. Then --sparse-only (below) only
    # embeds clauses STILL missing a posting — no session ever restarts from zero.
    # GRACEFUL: the first ever run (no channel) or any pull failure just proceeds with
    # an empty clause_sparse (degrade-not-block, CLAUDE.md §1).
    SPARSE_IMG = "ghcr.io/%s/3gpp-sparse:latest" % OWNER
    carried = 0
    if sh('printf %%s "$GHCR_PAT" | oras login ghcr.io -u "%s" --password-stdin' % OWNER).returncode != 0:
        print("resume: oras login failed — starting fresh (no carry-over)", flush=True)
    else:
        rdir = TMP + "/resume"
        os.makedirs(rdir, exist_ok=True)
        pull = sh('oras pull "%s" -o "%s"' % (SPARSE_IMG, rdir))
        zst = rdir + "/sparse-shard.duckdb.zst"
        if pull.returncode == 0 and os.path.isfile(zst):
            if sh('zstd -d -f "%s" -o "%s/sparse-shard.duckdb"' % (zst, rdir)).returncode == 0:
                ov = sh('%s/overlay --base %s --vec %s/sparse-shard.duckdb' % (TMP, DB, rdir))
                print("OVERLAY:", (ov.stdout or "")[-300:], (ov.stderr or "")[-300:], flush=True)
                if ov.returncode == 0:
                    carried = sh('duckdb "%s" -noheader -list '
                                 '"SELECT count(*) FROM clause_sparse;"' % DB).stdout.strip() or "0"
                    res("resume_carried_postings=%s" % carried)
                else:
                    res("warn resume_overlay_failed")
            else:
                print("resume: zstd decompress failed — starting fresh", flush=True)
        else:
            print("resume: no published sparse shard yet — starting fresh", flush=True)

    # --- 5. Run --sparse-only over the corpus (additive; resumable; BOUNDED) ----
    # EMBED_LIMIT bounds THIS session to a slice that fits under Kaggle's 12 h cap;
    # ClausesMissingSparse skips whatever the 4.5 overlay already carried, so the
    # work-list is "the remaining gap", not the whole corpus. Re-dispatch until the
    # RESULT shows clause_sparse_rows == clauses_total.
    eenv = benv.copy()
    eenv["EMBED_MODELS_CONFIG"] = TMP + "/models.yaml"
    eenv["EMBED_MODEL"] = "bge-m3-sparse"
    eenv["ORT_EP"] = "cuda"
    lim = (" --limit %s" % LIMIT) if (LIMIT and LIMIT != "0") else ""
    r = sh('%s/embed --db %s --sparse-only --sparse-batch %s%s' % (TMP, DB, SPARSE_BATCH, lim), env=eenv)
    print("EMBED STDOUT:", (r.stdout or "")[-600:], flush=True)
    print("EMBED STDERR:", (r.stderr or "")[-600:], flush=True)
    if r.returncode != 0:
        res("fail sparse_only rc=%d" % r.returncode)
        sys.exit(0)

    # --- 6. Convergence count + sparse identity + the CUMULATIVE shard --------
    # No 2nd embed pass: runSparse already stamped sparse_model at the end of the pass
    # (cmd/embed/main.go), and an unbounded 2nd pass would re-embed the whole corpus on
    # a bounded slice. Convergence is read directly: rows == total ⇒ corpus fully sparse.
    def scalar(q):
        return sh('duckdb "%s" -noheader -list "%s"' % (DB, q)).stdout.strip()

    populated = scalar("SELECT count(DISTINCT chunk_id) FROM clause_sparse;") or "0"
    total = scalar("SELECT count(*) FROM clauses WHERE text <> '';") or "0"
    sm = scalar("SELECT value FROM schema_meta WHERE key='sparse_model';")
    res("clause_sparse_rows=%s clauses_total=%s converged=%s"
        % (populated, total, populated == total and total != "0"))
    with open(WORK + "/sparse-index.json", "w") as sf:
        sf.write('{"sparse_model":"%s"}\n' % sm)

    # Compact FOLDABLE shard the bake/next-resume folds via cmd/overlay (overlaySparse
    # re-keys by NATURAL IDENTITY: spec_id, release, clause_path, text — robust to
    # chunk_id reshuffles). It carries clauses(identity + text) AND clause_sparse for
    # EVERY clause with a posting (carried-over + this session's delta) — CUMULATIVE,
    # so re-publishing advances the campaign. No HNSW, no catalogue → a fraction of the
    # DB. The embedding / embedding_hash columns are kept (as NULL) ONLY so cmd/overlay's
    # dense UPDATE (which reads s.embedding) type-checks; NULL means 0 vectors overlaid.
    SHARD = WORK + "/sparse-shard.duckdb"
    if os.path.exists(SHARD):
        os.remove(SHARD)  # duckdb won't CREATE TABLE into an existing file
    shard_sql = (
        "ATTACH '%s' AS f (READ_ONLY); "
        "CREATE TABLE clauses AS SELECT chunk_id, spec_id, release, version, clause_path, text, "
        "CAST(NULL AS FLOAT[1024]) AS embedding, CAST(NULL AS VARCHAR) AS embedding_hash "
        "FROM f.clauses WHERE chunk_id IN (SELECT chunk_id FROM f.clause_sparse); "
        "CREATE TABLE clause_sparse AS SELECT chunk_id, term_id, weight FROM f.clause_sparse;"
    ) % DB
    if sh('duckdb "%s" "%s"' % (SHARD, shard_sql)).returncode != 0:
        res("fail shard_export")
        sys.exit(0)
    res("DONE sparse_model=%s rows=%s/%s shard=%s" % (
        sm, populated, total, os.path.exists(SHARD)))
    sys.exit(0)
except Exception as e:
    import traceback
    traceback.print_exc()
    res("fail exception=%s %s" % (type(e).__name__, str(e)[:200]))
    sys.exit(0)
