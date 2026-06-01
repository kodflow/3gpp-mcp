# Kaggle GPU kernel for the embed campaign — NATIVE PYTHON (Kaggle kernels only
# accept language python/r/rmarkdown, never bash). ZERO-UPLOAD + RESUMABLE +
# self-diagnosing: everything is pulled from PUBLIC sources on the kernel (Internet
# on). Writes compact single-line "RESULT ..." markers to /kaggle/working/RESULT.txt
# (the driver reads those; full stdout has noise).
#
# Config comes from os.environ — the driver (kaggle-embed-campaign.sh) PREPENDS a few
# os.environ.setdefault(...) lines before this body, since Kaggle passes no env.
#
# Resume model (load-bearing for the 12h cap): each series keeps ONE output Kaggle
# Dataset ${KAGGLE_USERNAME}/3gpp-embedded-s<NN> with 3gpp-embedded.duckdb. START:
# if mounted as input, resume from it (embedding_hash skips done clauses); else build
# a fresh lexical slice from the published `latest` DB. END: version the partial DB
# back (driver does it from the laptop, or this tail if Kaggle Secrets are present).
import glob
import json
import os
import re
import shutil
import subprocess
import sys
import threading
import time

WORK = "/kaggle/working"
R = os.path.join(WORK, "RESULT.txt")
os.makedirs(WORK, exist_ok=True)
open(R, "w").close()


def say(msg):
    line = "RESULT " + msg
    print(line, flush=True)
    with open(R, "a") as f:
        f.write(line + "\n")


def fail(code, detail=""):
    say("FAIL=%s detail=%s" % (code, detail))
    sys.exit(1)


def sh(cmd, env=None, timeout=None):
    """Run a shell command, capturing text output. Returns CompletedProcess."""
    return subprocess.run(cmd, shell=True, text=True, capture_output=True,
                          env=env, timeout=timeout)


def have(cmd):
    return shutil.which(cmd) is not None


def duckdb_scalar(db, query):
    return sh('duckdb "%s" -noheader -list "%s"' % (db, query)).stdout.strip()


REPO = "https://github.com/kodflow/3gpp-mcp"
BRANCH = os.environ.get("BRANCH", "main")
FLOOR = os.environ.get("EMBED_FLOOR", "Rel-17")
SERIES = os.environ.get("SERIES", "21")
BGE_COMMIT = "5617a9f61b028005a4858fdac845db406aefb181"
# fp32 (default, proven) | fp16 (half precision: ~2-6x faster on T4 Tensor Cores).
# fp16 is produced by converting OUR pinned fp32 ONNX in-kernel (keep_io_types) so the
# graph/IO nodes stay identical — only the weights become fp16; the EmbedIdentity flips
# to fp16 so it never mixes with fp32 in one HNSW.
PRECISION = os.environ.get("EMBED_PRECISION", "fp32").lower()
ORT_VERSION = os.environ.get("ORT_VERSION", "1.26.0")
CHECKPOINT_EVERY = os.environ.get("CHECKPOINT_EVERY", "2000")
# Stop ~10.8h in so the version/validate tail always runs before Kaggle's 12h kill.
TIME_BUDGET = int(os.environ.get("EMBED_TIME_BUDGET", "39000"))
os.chdir(WORK)
os.environ.pop("EMBEDDER", None)  # force the real onnx/CUDA backend (never Local)

say("step=start floor=%s series=%s precision=%s ort=%s branch=%s budget=%ds"
    % (FLOOR, SERIES, PRECISION, ORT_VERSION, BRANCH, TIME_BUDGET))

# GPU-optional: CUDA when a GPU is attached (T4 Tensor-Core fp16 path), else CPU so
# the pipeline + download path are still exercised end-to-end.
g = sh("nvidia-smi -L")
if g.returncode == 0 and g.stdout.strip():
    EP = "cuda"
    say("gpu=present detail=%s ep=cuda" % g.stdout.splitlines()[0].strip())
else:
    EP = "cpu"
    say("gpu=absent ep=cpu (CPU fallback — GPU not attached to this worker)")

sh("apt-get update -qq && apt-get install -y -qq zstd unzip")
if not have("zstd"):
    fail("no_zstd")

# Go toolchain: Kaggle's image ships an OLDER Go (e.g. go1.23.x) and would let
# GOTOOLCHAIN=auto silently re-download the go.mod toolchain at build time — leaving
# `go version` misleading and the build non-deterministic. Install EXACTLY the version
# go.mod declares (fetched from raw GitHub so it auto-tracks future bumps; pinned
# fallback if the lookup fails), ALWAYS prepend it to PATH so it shadows the
# preinstalled Go, and pin GOTOOLCHAIN=local so the build uses it with no hidden fetch.
GO_FALLBACK = "1.26.3"
_gomod = sh('curl -fsSL --retry 5 "%s/raw/%s/go.mod"' % (REPO, BRANCH)).stdout
_m = re.search(r"(?m)^go[ \t]+([0-9]+\.[0-9]+\.[0-9]+)", _gomod)
GO_VER = _m.group(1) if _m else GO_FALLBACK
if sh("curl -fsSL --retry 5 https://go.dev/dl/go%s.linux-amd64.tar.gz -o /tmp/go.tgz"
      % GO_VER).returncode != 0:
    fail("go_dl", "version=%s" % GO_VER)
sh("rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz")
os.environ["PATH"] = "/usr/local/go/bin:" + os.environ.get("PATH", "")
os.environ["GOTOOLCHAIN"] = "local"  # use the installed toolchain; never silently fetch
gv = sh("go version").stdout.split()
say("go=%s want=go%s" % (gv[2] if len(gv) > 2 else "?", GO_VER))

if sh("curl -fsSL --retry 5 -o /tmp/duckdb.zip "
      "https://github.com/duckdb/duckdb/releases/download/v1.1.3/duckdb_cli-linux-amd64.zip").returncode != 0:
    fail("duckdb_dl")
os.makedirs("/tmp/bin", exist_ok=True)
sh("cd /tmp/bin && unzip -o -q /tmp/duckdb.zip")
os.environ["PATH"] = "/tmp/bin:" + os.environ.get("PATH", "")
if sh("duckdb --version").returncode != 0:
    fail("duckdb_run")

src = os.path.join(WORK, "src")
clone = sh('git clone --depth 1 -b "%s" "%s" "%s"' % (BRANCH, REPO, src))
if clone.returncode != 0:
    # tarball fallback (git clone hit transient DNS on some workers).
    if sh('curl -fsSL --retry 5 -o /tmp/src.tgz "%s/archive/refs/heads/%s.tar.gz"' % (REPO, BRANCH)).returncode == 0:
        os.makedirs(src, exist_ok=True)
        sh('tar -C "%s" --strip-components=1 -xzf /tmp/src.tgz' % src)
    if not os.path.isdir(os.path.join(src, "cmd")):
        fail("clone", (clone.stderr or "").strip()[-120:])
os.chdir(src)

# ---- RESUME or fresh SLICE -------------------------------------------------
EMBEDDED_DB = os.path.join(WORK, "3gpp-embedded.duckdb")
RESUME_DB = os.environ.get(
    "RESUME_DB", "/kaggle/input/3gpp-embedded-s%s/3gpp-embedded.duckdb" % SERIES)
if os.path.isfile(RESUME_DB):
    shutil.copy(RESUME_DB, EMBEDDED_DB)
    prior = duckdb_scalar(EMBEDDED_DB, "SELECT count(*) FROM clauses WHERE embedding IS NOT NULL;")
    say("resume=present src=%s prior_embedded=%s" % (RESUME_DB, prior or "?"))
else:
    say("resume=absent (fresh slice from published latest)")
    if sh('curl -fsSL --retry 5 -o "%s/full.zst" "%s/releases/download/latest/3gpp.duckdb.zst"'
          % (WORK, REPO)).returncode != 0:
        fail("db_dl")
    if sh('zstd -d --long=27 -f "%s/full.zst" -o "%s/full.duckdb"' % (WORK, WORK)).returncode != 0:
        fail("decompress")
    fulln = duckdb_scalar("%s/full.duckdb" % WORK, "SELECT count(*) FROM clauses;")
    say("full_clauses=%s" % fulln)
    slice_sql = ("ATTACH '%s/full.duckdb' AS s (READ_ONLY); "
                 "CREATE TABLE clauses AS SELECT * FROM s.clauses "
                 "WHERE substr(spec_id,1,2)='%s';" % (WORK, SERIES))
    if sh('duckdb "%s" "%s"' % (EMBEDDED_DB, slice_sql)).returncode != 0:
        fail("slice")
    sln = duckdb_scalar(EMBEDDED_DB, "SELECT count(*) FROM clauses;")
    say("sliced_clauses=%s" % sln)
    if not (sln.isdigit() and int(sln) >= 1):
        fail("empty_slice", "series=%s full=%s" % (SERIES, fulln))

# ---- ONNX Runtime (GPU) + BGE-M3 -------------------------------------------
if sh('curl -fsSL --retry 5 -o /tmp/ort.tgz '
      '"https://github.com/microsoft/onnxruntime/releases/download/v%s/onnxruntime-linux-x64-gpu-%s.tgz"'
      % (ORT_VERSION, ORT_VERSION)).returncode != 0:
    fail("ort_dl")
os.makedirs("%s/ort" % WORK, exist_ok=True)
if sh('tar -C "%s/ort" --strip-components=1 -xzf /tmp/ort.tgz' % WORK).returncode != 0:
    fail("ort_untar")
libs = glob.glob("%s/ort/**/libonnxruntime.so*" % WORK, recursive=True)
if not libs:
    fail("ort_nolib")
ORT_LIB = libs[0]
ORT_DIR = os.path.dirname(ORT_LIB)
say("ort_lib=%s" % os.path.basename(ORT_LIB))

BGE = "%s/bge-m3" % WORK
os.makedirs(BGE, exist_ok=True)
HF = "https://huggingface.co/BAAI/bge-m3/resolve/%s" % BGE_COMMIT
for url, dest, code in [
    ("%s/onnx/model.onnx" % HF, "%s/model.onnx" % BGE, "hf_model"),
    ("%s/onnx/model.onnx_data" % HF, "%s/model.onnx_data" % BGE, "hf_data"),
    ("%s/onnx/Constant_7_attr__value" % HF, "%s/Constant_7_attr__value" % BGE, "hf_const"),
    ("%s/tokenizer.json" % HF, "%s/tokenizer.json" % BGE, "hf_tok"),
]:
    if sh('curl -fsSL --retry 5 "%s" -o "%s"' % (url, dest)).returncode != 0:
        fail(code)
say("model_data_bytes=%d" % os.path.getsize("%s/model.onnx_data" % BGE))

# ---- optional fp16: convert OUR fp32 ONNX in-kernel (keep_io_types) ---------
# Converting the SAME graph (vs fetching a third-party fp16 export) guarantees the
# input/output nodes stay input_ids/attention_mask -> sentence_embedding, so the Go
# embedder is unchanged; only the float weights become fp16. keep_io_types keeps the
# output fp32 (the Go side reads []float32). A kernel-local models.yaml (absolute dir,
# precision: fp16) selects it via EMBED_MODELS_CONFIG/EMBED_MODEL.
BGE16 = "%s/bge-m3-fp16" % WORK
MODEL_DIR_ACTIVE = BGE
EMBED_ENV = {}
if PRECISION == "fp16":
    say("fp16=convert start (src=%s)" % BGE)
    if sh("pip install --quiet onnx onnxconverter_common").returncode != 0:
        fail("fp16_deps")
    os.makedirs(BGE16, exist_ok=True)
    conv_py = "%s/convfp16.py" % WORK
    with open(conv_py, "w") as cf:
        cf.write(
            "import onnx\n"
            "from onnx import shape_inference\n"
            "from onnxconverter_common import float16\n"
            # File-based shape inference FIRST: BGE-M3 is >2GB so in-memory shape\n"
            # inference (what convert_float_to_float16 runs unless disabled) hits the\n"
            # 2GB protobuf ceiling and is skipped — leaving the converter blind to\n"
            # tensor types at fp16/fp32 boundaries, so it omits a Cast and ORT later\n"
            # rejects the graph at a node like /model/Sub (type float16 vs float).\n"
            # infer_shapes_path streams from disk (no 2GB limit) and writes the graph\n"
            # next to the external-data files so refs still resolve; the populated\n"
            # value_info then drives correct Cast placement under disable_shape_infer.\n"
            "shape_inference.infer_shapes_path(%r, %r)\n"
            "m = onnx.load(%r)\n"
            "m16 = float16.convert_float_to_float16(m, keep_io_types=True, disable_shape_infer=True)\n"
            "onnx.save(m16, %r, save_as_external_data=True, all_tensors_to_one_file=True, location='model.onnx_data')\n"
            "print('fp16_saved')\n"
            % ("%s/model.onnx" % BGE, "%s/model.shp.onnx" % BGE,
               "%s/model.shp.onnx" % BGE, "%s/model.onnx" % BGE16)
        )
    conv = sh('python3 "%s"' % conv_py)
    if conv.returncode != 0 or not os.path.isfile("%s/model.onnx" % BGE16):
        fail("fp16_convert", ((conv.stderr or "") + (conv.stdout or "")).replace("\n", " ")[-1500:])
    shutil.copy("%s/tokenizer.json" % BGE, "%s/tokenizer.json" % BGE16)
    with open("%s/models.yaml" % WORK, "w") as mf:
        mf.write(
            "active: bge-m3-fp16\n"
            "models:\n"
            "  - name: bge-m3-fp16\n"
            "    family: bge-m3\n"
            "    dir: %s\n"
            "    precision: fp16\n"
            "    dim: 1024\n"
            "    normalization: l2\n"
            "    revision: %s\n"
            "    tokenizer_revision: %s\n"
            "    tokenizer_dir: %s\n"
            "    inputs: [input_ids, attention_mask]\n"
            "    output: sentence_embedding\n"
            % (BGE16, BGE_COMMIT[:7], BGE_COMMIT[:7], BGE16)
        )
    MODEL_DIR_ACTIVE = BGE16
    EMBED_ENV = {"EMBED_MODELS_CONFIG": "%s/models.yaml" % WORK, "EMBED_MODEL": "bge-m3-fp16"}
    say("fp16=ready bytes=%d" % os.path.getsize("%s/model.onnx_data" % BGE16))

# ---- build the onnx embed binary -------------------------------------------
env = dict(os.environ)
env["CGO_ENABLED"] = "1"
env["ONNXRUNTIME_SHARED_LIBRARY_PATH"] = ORT_LIB
env["LD_LIBRARY_PATH"] = ORT_DIR + ":" + env.get("LD_LIBRARY_PATH", "")
build = sh('go build -tags onnx -o "%s/embed" ./cmd/embed' % WORK, env=env)
if build.returncode != 0:
    fail("build", (build.stderr or "").replace("\n", " ")[-200:])
say("build=ok")

# ---- EMBED (recent-first, resumable, time-bounded) -------------------------
env["EMBED_MODEL_DIR"] = MODEL_DIR_ACTIVE
env["ORT_EP"] = EP
env["EMBED_GRAPH_OPT"] = "1"
env.update(EMBED_ENV)  # fp16: EMBED_MODELS_CONFIG + EMBED_MODEL=bge-m3-fp16
REPORT = "%s/embed-report.json" % WORK
cmd = ('"%s/embed" --db "%s" --embed-floor "%s" --series "%s" '
       "--order recent --resume --checkpoint-every \"%s\" --no-hnsw "
       "--require-semantic --report json"
       % (WORK, EMBEDDED_DB, FLOOR, SERIES, CHECKPOINT_EVERY))
start = time.time()
rc = 0
# Stream the embedder's progress (stderr) LIVE to the Kaggle log so a long GPU run
# shows `embed progress: N embedded (R cl/s)` instead of silence after build=ok. The
# JSON report still goes to REPORT (stdout); stderr is BOTH echoed and saved to
# /tmp/embed.err for the FAIL detail. A threading.Timer enforces the time budget even
# if the process stops emitting (a blocking read would never see the deadline); a
# timer-kill surfaces as a negative wait() status, mapped to the 124 timeout path.
with open(REPORT, "w") as out, open("/tmp/embed.err", "w") as errf:
    proc = subprocess.Popen(cmd, shell=True, env=env, stdout=out,
                            stderr=subprocess.PIPE, text=True, bufsize=1)
    killer = threading.Timer(TIME_BUDGET, proc.kill)
    killer.start()
    try:
        for line in proc.stderr:
            errf.write(line)
            s = line.rstrip()
            if s and ("progress" in s or "error" in s.lower() or "warn" in s.lower()):
                sys.stdout.write("embed| %s\n" % s[-180:])
                sys.stdout.flush()
        rc = proc.wait()
    finally:
        killer.cancel()
    if rc < 0:  # killed by the budget timer (-SIGKILL) → treat as time-budget hit
        rc = 124
elapsed = int(time.time() - start)
if rc == 124:
    say("embed=timeout elapsed=%ds (hit time budget — partial DB is resumable next run)" % elapsed)
elif rc != 0:
    err = ""
    try:
        err = open("/tmp/embed.err").read().replace("\n", " ")[-1500:]
    except Exception:
        pass
    say("embed_rc=%d err=%s" % (rc, err))
    fail("embed_run")

rep = {}
try:
    rep = json.load(open(REPORT))
except Exception:
    pass
MODEL = rep.get("model", "?")
EMB = int(rep.get("embedded_clauses", 0) or 0)
NUL = rep.get("null_embeddings_at_floor", None)
say("model=%s embedded=%d null_at_floor=%s elapsed=%ds ep=%s rc=%d"
    % (MODEL, EMB, NUL if NUL is not None else "?", elapsed, EP, rc))
if elapsed > 0:
    say("throughput=%.2f clauses_per_s_%s" % (EMB / elapsed, EP))

# ---- self-version the partial DB back to the resume dataset ----------------
# Only when creds are injected (Kaggle Secrets) AND something was embedded — avoids
# spamming empty versions. The driver also pulls `kernels output` and versions from
# the laptop (creds-never-leave-laptop posture); this tail is the unattended opt-in.
KU = os.environ.get("KAGGLE_USERNAME", "")
KK = os.environ.get("KAGGLE_KEY", "")
EMBED_STATE_DS = os.environ.get("EMBED_STATE_DS") or (("%s/3gpp-embedded-s%s" % (KU, SERIES)) if KU else "")
if KU and KK and EMB >= 1:
    if have("kaggle") or sh("pip install --quiet kaggle").returncode == 0:
        out = "%s/state" % WORK
        os.makedirs(out, exist_ok=True)
        shutil.copy(EMBEDDED_DB, "%s/3gpp-embedded.duckdb" % out)
        json.dump({"title": "3gpp-embedded-s%s" % SERIES, "id": EMBED_STATE_DS,
                   "licenses": [{"name": "CC0-1.0"}]},
                  open("%s/dataset-metadata.json" % out, "w"))
        if sh('kaggle datasets version -p "%s" -m "series=%s embedded=%d" --dir-mode zip'
              % (out, SERIES, EMB)).returncode == 0:
            say("versioned=%s" % EMBED_STATE_DS)
        elif sh('kaggle datasets create -p "%s" --dir-mode zip' % out).returncode == 0:
            say("created=%s" % EMBED_STATE_DS)
        else:
            say("version=skip detail=version_and_create_failed")
    else:
        say("version=skip detail=no_kaggle_cli")
else:
    say("version=skip detail=no_creds_or_nothing_embedded embedded=%d" % EMB)

# A complete series ends with null_at_floor==0 AND no timeout. A timeout/partial is a
# SUCCESSFUL increment — the driver re-launches to continue.
if rc == 0 and str(NUL) == "0":
    say("step=OK complete=1")
else:
    say("step=OK complete=0 (partial — resume next run)")
