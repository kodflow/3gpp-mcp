# Corpus-scale SPARSE pass on Kaggle GPU (additive — never recomputes dense).
# Pulls the private lexical base (ghcr.io/<owner>/3gpp-corpus:latest) via crane,
# exports the dense+sparse BGE-M3 model (proven recipe, kernel-sparse-embed-smoke.py),
# builds the Rust sparse pass (embed-core-sparse + store-rs overlay/embed-io) on ORT-CUDA, then:
#   RESUME — pulls the previously published sparse layer (ghcr.io/<owner>/3gpp-sparse
#            :latest, if any) and overlays its clause_sparse onto the fresh base, so
#            this session only embeds clauses STILL missing a posting.
#   PASS   — the Rust sparse pass (embed-core-sparse, the proven dual-head sparse arm —
#            replaces the deleted Go cmd/embed --sparse-only, Phase 11b): store-rs embed-io
#            --export-sparse-worklist (a BOUNDED --limit slice that fits under Kaggle's 12 h
#            cap; clauses_needing_sparse skips already-populated clauses) → embed-core-sparse
#            → embed-io --import-sparse into clause_sparse.
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
# MUST match the onnxruntime ABI that `ort = 2.0.0-rc.9` expects (embed-core is built
# load-dynamic and dlopens this via ORT_DYLIB_PATH). ort rc.9 targets onnxruntime 1.20.x;
# pointing it at a newer lib (1.26.0) makes session.run hang at the first inference. Keep
# this pinned in lockstep with the `ort` version in rust/embed-core/Cargo.toml.
ORT_VERSION = "1.20.1"
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


def sh_live(c, env=None, timeout=None):
    """Run a long command with stdout/stderr INHERITED (streamed live to the kernel log,
    not buffered like sh()'s capture_output) and a hard timeout self-watchdog. Returns the
    exit code, or 124 if the timeout fired (process killed). Used for the multi-hour embed
    so its per-batch progress is visible AND a stalled/overrunning run self-terminates
    instead of burning the full 12 h Kaggle cap — Kaggle has no external stop API."""
    print("+", c, "(streamed, timeout=%ss)" % timeout, flush=True)
    try:
        return subprocess.run(c, shell=True, text=True, env=env or os.environ.copy(),
                              timeout=timeout).returncode
    except subprocess.TimeoutExpired:
        print("TIMEOUT: killed after %ss — proceeding to import partial postings" % timeout, flush=True)
        return 124


def run_parallel_live(cmds, env=None, timeout=None):
    """Launch all cmds CONCURRENTLY with stdout/stderr inherited (live), share one deadline.
    Returns the list of exit codes (124 for any process killed by the timeout). Used to fan the
    sparse embed across every GPU (one process per device) — works for 1, 2 or N GPUs."""
    import time
    for c in cmds:
        print("+ (parallel)", c, flush=True)
    procs = [subprocess.Popen(c, shell=True, text=True, env=env or os.environ.copy()) for c in cmds]
    rcs = [None] * len(procs)
    deadline = (time.monotonic() + timeout) if timeout else None
    while any(r is None for r in rcs):
        for i, p in enumerate(procs):
            if rcs[i] is None and p.poll() is not None:
                rcs[i] = p.returncode
        if deadline and time.monotonic() > deadline:
            print("TIMEOUT: killing %d embed process(es) after %ss" % (len(procs), timeout), flush=True)
            for i, p in enumerate(procs):
                if rcs[i] is None:
                    p.kill()
                    rcs[i] = 124
            break
        if any(r is None for r in rcs):
            time.sleep(2)
    return rcs


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
    pi = sh("pip -q install FlagEmbedding onnx onnxruntime onnxscript onnxconverter-common")
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

    # Example with batch=2 + two DIFFERENT lengths so the exporter sees both axes vary.
    enc = m3.tokenizer(["AMF SMF N4 registration procedure", "short"],
                       return_tensors="pt", padding=True, truncation=True, max_length=8192)
    args = (enc["input_ids"], enc["attention_mask"])
    # dynamo=True IGNORES dynamic_axes (it warns and bakes the example's batch=1 → batched
    # inference then dies with 'MatMul dimension mismatch'). Declare DYNAMIC batch+seq the
    # dynamo-native way via dynamic_shapes (torch.export.Dim), shared across both inputs.
    from torch.export import Dim
    _batch = Dim("batch", min=1, max=512)
    _seq = Dim("seq", min=1, max=8192)
    common = dict(
        input_names=["input_ids", "attention_mask"],
        output_names=["sentence_embedding", "sparse_weights"],
        dynamic_shapes={"input_ids": {0: _batch, 1: _seq},
                        "attention_mask": {0: _batch, 1: _seq}},
        opset_version=18)
    # BGE-M3 fp32 weights are ~2.2 GB, OVER the 2 GB ONNX protobuf limit → they MUST be
    # written as external data (model.onnx_data). An inline export produces a corrupt model
    # that HANGS embed-core at load (the 4h "Output 0 B" spins). prog.save(external_data=True)
    # did NOT externalize, so write the in-memory proto via onnx.save_model with
    # save_as_external_data=True (the proto has no 2 GB limit until it hits disk).
    model = DenseSparse(backbone, sparse_linear).eval()
    import onnx
    onnx_path = MDIR + "/model.onnx"
    try:
        # Newer torch: f-path + external_data=True writes both .onnx and .onnx_data directly.
        torch.onnx.export(model, args, onnx_path, dynamo=True, external_data=True, **common)
    except TypeError:
        # Older torch: capture the ONNXProgram and externalize via onnx.save_model.
        prog = torch.onnx.export(model, args, dynamo=True, **common)
        proto = getattr(prog, "model_proto", None) or prog.model
        onnx.save_model(proto, onnx_path, save_as_external_data=True,
                        all_tensors_to_one_file=True, location="model.onnx_data", size_threshold=1024)
    # Belt-and-braces: if external data still didn't land (some torch builds inline it),
    # reload + rewrite with onnx.save_model to force the .onnx_data sidecar.
    if not os.path.exists(MDIR + "/model.onnx_data"):
        try:
            proto = onnx.load(onnx_path)  # only succeeds if the .onnx is <2 GB / loadable
            onnx.save_model(proto, onnx_path, save_as_external_data=True,
                            all_tensors_to_one_file=True, location="model.onnx_data", size_threshold=1024)
        except Exception as e:
            print("external-data rewrite failed (%s)" % e, flush=True)
    # fp16: BGE-M3 sparse (lexical) weights are robust to half precision and the T4 has fp16
    # tensor cores → ~2-4x faster than fp32 (fp32 was only chosen to de-risk the export). Convert
    # the ONNX weights to fp16 with keep_io_types=True so the model I/O STAYS fp32 — embed-core
    # reads fp32 unchanged, only the internal compute is fp16. Halves the model to ~1.1 GB too.
    # GRACEFUL: any conversion failure keeps the working fp32 model untouched (degrade-not-block).
    try:
        from onnxconverter_common import float16
        m16 = float16.convert_float_to_float16(
            onnx.load(onnx_path), keep_io_types=True, disable_shape_infer=True)
        os.remove(onnx_path)
        try:
            os.remove(MDIR + "/model.onnx_data")
        except OSError:
            pass
        onnx.save_model(m16, onnx_path, save_as_external_data=True,
                        all_tensors_to_one_file=True, location="model.onnx_data", size_threshold=1024)
        res("model_fp16=ok")
    except Exception as e:
        res("model_fp16=skipped(%s)_keeping_fp32" % str(e)[:100].replace(" ", "_"))
    m3.tokenizer.save_pretrained(MDIR)
    has_data = os.path.exists(MDIR + "/model.onnx_data")
    res("model_exported has_data=%s" % has_data)
    # FAIL-FAST: without the external-data sidecar a >2GB model is unloadable; abort here
    # (a few minutes) instead of hanging hours in embed-core at the first inference.
    if not has_data:
        res("fail model_export_no_external_data (>2GB model needs model.onnx_data)")
        sys.exit(0)

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

    # --- 4a. PREBUILT FAST PATH: download the Rust bins built in CI (rust-bins release,
    # glibc-2.35 base → run on Kaggle) instead of burning ~18 min of GPU re-compiling
    # identical code. Match on the git TREE sha of rust/ (content of the whole rust/ subtree)
    # so unrelated commits don't force a rebuild — only an actual rust/ change does. Fall back
    # to from-source if absent/mismatched (correctness never depends on the artifact). All
    # three bins are ort load-dynamic → portable; they dlopen the onnxruntime downloaded above.
    prebuilt = False
    rust_tree = (sh("cd %s && git rev-parse HEAD:rust" % src).stdout or "").strip()
    rel = "%s/releases/download/rust-bins" % REPO
    bins_tree = ""
    if sh('curl -fsSL --retry 3 -o %s/RUST_TREE "%s/RUST_TREE"' % (TMP, rel)).returncode == 0 \
            and os.path.exists(TMP + "/RUST_TREE"):
        bins_tree = open(TMP + "/RUST_TREE").read().strip()
    if bins_tree and bins_tree == rust_tree:
        ok = True
        for name in ("overlay", "embed-io", "embed-core-sparse"):
            if sh('curl -fsSL --retry 5 -o %s/%s "%s/%s"' % (TMP, name, rel, name)).returncode != 0:
                ok = False
                break
        if ok:
            sh("chmod +x %s/overlay %s/embed-io %s/embed-core-sparse" % (TMP, TMP, TMP))
            sh("cp %s/embed-core-sparse %s/sparse-embed" % (TMP, TMP))
            prebuilt = True
            res("rust_bins=prebuilt rust_tree=%s" % rust_tree[:12])
    if not prebuilt:
        res("rust_bins=building_from_source bins_tree=%s rust_tree=%s"
            % (bins_tree[:12] or "none", rust_tree[:12]))
        # The Kaggle image ships Go but NOT cargo — install Rust before the store-rs +
        # embed-core builds below. Without this the cargo builds fail with "cargo: not found".
        if sh("command -v cargo >/dev/null 2>&1").returncode != 0:
            sh("curl -fsSL https://sh.rustup.rs | sh -s -- -y --default-toolchain stable --profile minimal")
        os.environ["PATH"] = os.path.expanduser("~/.cargo/bin") + ":" + os.environ.get("PATH", "")
        if sh("command -v cargo >/dev/null 2>&1").returncode != 0:
            res("fail no_cargo")
            sys.exit(0)
        print("cargo:", (sh("cargo --version").stdout or "").strip()[:40], flush=True)
    benv = os.environ.copy()
    benv["CGO_ENABLED"] = "1"
    benv["ONNXRUNTIME_SHARED_LIBRARY_PATH"] = ORT_LIB
    benv["LD_LIBRARY_PATH"] = ORT_DIR + ":" + benv.get("LD_LIBRARY_PATH", "")
    if not prebuilt:
        # store-rs bins: overlay (RESUME carry-over) + embed-io (sparse worklist export +
        # clause_sparse import). libduckdb is statically bundled (same engine the round-trip CI
        # proves byte-compatible with the Go serve side).
        bo = sh('cd %s && cargo build --release --manifest-path rust/store/Cargo.toml --bin overlay --bin embed-io '
                '&& cp rust/target/release/overlay rust/target/release/embed-io %s/' % (src, TMP), env=benv)
        if bo.returncode != 0:
            res("fail build_store_bins " + (bo.stderr or "")[-400:])
            sys.exit(0)
        # The bulk sparse embedder: embed-core-sparse drives the SAME proven dual-head sparse arm
        # the serve path uses (embed_core_embed_sparse). Replaces the deleted Go cmd/embed
        # --sparse-only (Phase 11b). load-dynamic ort dlopens libonnxruntime via ORT_DYLIB_PATH.
        bs = sh('cd %s && cargo build --release --manifest-path rust/embed-core/Cargo.toml --bin embed-core-sparse --features ort,cuda '
                '&& cp rust/embed-core/target/release/embed-core-sparse %s/sparse-embed' % (src, TMP), env=benv)
        if bs.returncode != 0:
            res("fail build_sparse_embed " + (bs.stderr or "")[-400:])
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

    # --- 5. Rust sparse pass over the corpus (additive; resumable; BOUNDED) -----
    # EMBED_LIMIT bounds THIS session to a slice that fits under Kaggle's 12 h cap;
    # ClausesMissingSparse skips whatever the 4.5 overlay already carried, so the
    # work-list is "the remaining gap", not the whole corpus. Re-dispatch until the
    # RESULT shows clause_sparse_rows == clauses_total.
    eenv = benv.copy()
    eenv["EMBED_MODELS_CONFIG"] = TMP + "/models.yaml"
    eenv["EMBED_MODEL"] = "bge-m3-sparse"
    eenv["EMBED_MODEL_DIR"] = MDIR        # embed-core-sparse loads the dual-head model here
    eenv["ORT_DYLIB_PATH"] = ORT_LIB      # load-dynamic onnxruntime for the cdylib/bin
    eenv["ORT_EP"] = "cuda"
    # Resolve the sparse identity (CGO-free Go) to stamp meta sparse_model — the data-contract
    # --require-sparse gate + rust/discover --sparse-check match against it.
    sid = sh('cd %s && EMBED_MODEL=bge-m3-sparse EMBED_MODELS_CONFIG=%s/models.yaml CGO_ENABLED=0 '
             'go run ./cmd/embedid --sparse' % (src, TMP), env=eenv).stdout.strip()
    lim = (" --limit %s" % LIMIT) if (LIMIT and LIMIT != "0") else ""
    # 3-step Rust sparse pass: export the remaining sparse gap → embed via the dual-head arm →
    # import the postings into clause_sparse. Bounded by --limit on the worklist; resumable
    # (export skips clauses already in clause_sparse, the embedder skips ids already in --out).
    we = sh('%s/embed-io --db %s --export-sparse-worklist %s/sparse-wl.jsonl%s' % (TMP, DB, TMP, lim), env=eenv)
    if we.returncode != 0:
        res("fail export_sparse_worklist " + (we.stderr or "")[-400:])
        sys.exit(0)
    # STREAMED live (per-batch progress is visible — a buffered capture made past runs look
    # "stuck" though they were working) + a hard timeout self-watchdog (default 9 h, under the
    # 12 h cap). On timeout we keep going: embed-core-sparse writes postings incrementally, so
    # the partial --out is valid and publish=false accumulates it; the next slice resumes.
    EMBED_TIMEOUT = int(os.environ.get("SPARSE_EMBED_TIMEOUT", "32400"))
    POST = "%s/sparse-post.jsonl" % TMP
    # AUTO multi-GPU: one embed-core-sparse process per GPU (ONNX Runtime uses a single device
    # per process, so N GPUs need N processes). nvidia-smi -L lists the devices; we round-robin
    # the worklist into N shards and pin each process to one GPU via CUDA_VISIBLE_DEVICES, then
    # concat the shard outputs. ngpu=1 → exactly the old single-process path. Scales to any N.
    ngpu = 1
    try:
        ngpu = max(1, int(sh("nvidia-smi -L 2>/dev/null | wc -l").stdout.strip() or "1"))
    except Exception:
        ngpu = 1
    if ngpu <= 1:
        rc = sh_live('%s/sparse-embed --in %s/sparse-wl.jsonl --out %s'
                     % (TMP, TMP, POST), env=eenv, timeout=EMBED_TIMEOUT)
        rcs = [rc]
    else:
        # Round-robin split (balances long/short clauses across GPUs).
        shards = ["%s/sparse-wl.%d.jsonl" % (TMP, i) for i in range(ngpu)]
        wf = [open(s, "w") for s in shards]
        with open("%s/sparse-wl.jsonl" % TMP) as f:
            for n, line in enumerate(f):
                wf[n % ngpu].write(line)
        for h in wf:
            h.close()
        posts = ["%s/sparse-post.%d.jsonl" % (TMP, i) for i in range(ngpu)]
        cmds = ['CUDA_VISIBLE_DEVICES=%d %s/sparse-embed --in %s --out %s'
                % (i, TMP, shards[i], posts[i]) for i in range(ngpu)]
        print("multi-GPU sparse: %d process(es), one per GPU" % ngpu, flush=True)
        rcs = run_parallel_live(cmds, env=eenv, timeout=EMBED_TIMEOUT)
        # Concat shard outputs into the single POST the importer reads.
        with open(POST, "w") as out:
            for p in posts:
                if os.path.exists(p):
                    with open(p) as pf:
                        out.write(pf.read())
    if any(r == 124 for r in rcs):
        res("warn sparse_embed_timeout_partial rcs=%s" % rcs)  # import what was written
    elif all(r not in (0,) for r in rcs) and (not os.path.exists(POST) or os.path.getsize(POST) == 0):
        res("fail sparse_embed rcs=%s" % rcs)
        sys.exit(0)
    ri = sh('%s/embed-io --db %s --import-sparse %s --sparse-model "%s"' % (TMP, DB, POST, sid), env=eenv)
    print("IMPORT-SPARSE STDERR:", (ri.stderr or "")[-400:], flush=True)
    if ri.returncode != 0:
        res("fail import_sparse rc=%d" % ri.returncode)
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
