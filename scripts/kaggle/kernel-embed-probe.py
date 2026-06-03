# Kaggle 2×T4 PROBE kernel — measures the embed engine, never publishes anything.
# It A/Bs the new throughput levers (fixed-shape ladder + token-budget batching)
# against the old fixed-batch path on an IDENTICAL series-21 slice, with the
# per-batch profiler (EMBED_PROFILE=1) and a background nvidia-smi GPU-util sampler,
# so one run answers: (1) real cl/s uplift, (2) tokenize-vs-Run split, (3) whether
# the GPU is actually saturated. Writes compact "RESULT ..." markers the driver reads.
#
# Self-contained: pulls Go (go.mod version), DuckDB CLI, ONNX Runtime GPU, BGE-M3 and
# converts it to fp16 in-kernel (same recipe as kernel-embed.py). Internet ON, GPU ON.
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
    return subprocess.run(cmd, shell=True, text=True, capture_output=True, env=env, timeout=timeout)


def have(cmd):
    return shutil.which(cmd) is not None


def duckdb_scalar(db, query):
    return sh('duckdb "%s" -noheader -list "%s"' % (db, query)).stdout.strip()


REPO = "https://github.com/kodflow/3gpp-mcp"
BRANCH = os.environ.get("BRANCH", "feat/embed-pipeline-rebuild")
SERIES = os.environ.get("SERIES", "21")
FLOOR = os.environ.get("EMBED_FLOOR", "Rel-17")
LIMIT = os.environ.get("PROBE_LIMIT", "3000")
BGE_COMMIT = "5617a9f61b028005a4858fdac845db406aefb181"
PRECISION = os.environ.get("EMBED_PRECISION", "fp16").lower()
ORT_VERSION = os.environ.get("ORT_VERSION", "1.26.0")
os.chdir(WORK)
os.environ.pop("EMBEDDER", None)

say("step=start probe series=%s floor=%s limit=%s precision=%s ort=%s branch=%s"
    % (SERIES, FLOOR, LIMIT, PRECISION, ORT_VERSION, BRANCH))

g = sh("nvidia-smi -L")
if g.returncode == 0 and g.stdout.strip():
    EP = "cuda"
    gpus = [l for l in g.stdout.splitlines() if l.strip().startswith("GPU ")]
    say("gpu=present count=%d detail=%s ep=cuda" % (len(gpus), gpus[0].strip() if gpus else "?"))
else:
    EP = "cpu"
    say("gpu=absent ep=cpu")

sh("apt-get update -qq && apt-get install -y -qq zstd unzip")
if not have("zstd"):
    fail("no_zstd")

GO_FALLBACK = "1.26.3"
_gomod = sh('curl -fsSL --retry 5 "%s/raw/%s/go.mod"' % (REPO, BRANCH)).stdout
_m = re.search(r"(?m)^go[ \t]+([0-9]+\.[0-9]+\.[0-9]+)", _gomod)
GO_VER = _m.group(1) if _m else GO_FALLBACK
if sh("curl -fsSL --retry 5 https://go.dev/dl/go%s.linux-amd64.tar.gz -o /tmp/go.tgz" % GO_VER).returncode != 0:
    fail("go_dl", "version=%s" % GO_VER)
sh("rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz")
os.environ["PATH"] = "/usr/local/go/bin:" + os.environ.get("PATH", "")
os.environ["GOTOOLCHAIN"] = "local"
say("go=%s" % (sh("go version").stdout.strip()))

if sh("curl -fsSL --retry 5 -o /tmp/duckdb.zip "
      "https://github.com/duckdb/duckdb/releases/download/v1.1.3/duckdb_cli-linux-amd64.zip").returncode != 0:
    fail("duckdb_dl")
os.makedirs("/tmp/bin", exist_ok=True)
sh("cd /tmp/bin && unzip -o -q /tmp/duckdb.zip")
os.environ["PATH"] = "/tmp/bin:" + os.environ.get("PATH", "")

src = os.path.join(WORK, "src")
if sh('git clone --depth 1 -b "%s" "%s" "%s"' % (BRANCH, REPO, src)).returncode != 0:
    if sh('curl -fsSL --retry 5 -o /tmp/src.tgz "%s/archive/refs/heads/%s.tar.gz"' % (REPO, BRANCH)).returncode == 0:
        os.makedirs(src, exist_ok=True)
        sh('tar -C "%s" --strip-components=1 -xzf /tmp/src.tgz' % src)
    if not os.path.isdir(os.path.join(src, "cmd")):
        fail("clone")
os.chdir(src)

# ---- base slice from the published latest DB ------------------------------
BASE_DB = os.path.join(WORK, "base.duckdb")
if sh('curl -fsSL --retry 5 -o "%s/full.zst" "%s/releases/download/latest/3gpp.duckdb.zst"'
      % (WORK, REPO)).returncode != 0:
    fail("db_dl")
if sh('zstd -d --long=27 -f "%s/full.zst" -o "%s/full.duckdb"' % (WORK, WORK)).returncode != 0:
    fail("decompress")
slice_sql = ("ATTACH '%s/full.duckdb' AS s (READ_ONLY); "
             "CREATE TABLE clauses AS SELECT * FROM s.clauses WHERE substr(spec_id,1,2)='%s';" % (WORK, SERIES))
if sh('duckdb "%s" "%s"' % (BASE_DB, slice_sql)).returncode != 0:
    fail("slice")
say("sliced_clauses=%s" % duckdb_scalar(BASE_DB, "SELECT count(*) FROM clauses;"))

# ---- ONNX Runtime GPU + BGE-M3 + fp16 convert -----------------------------
if sh('curl -fsSL --retry 5 -o /tmp/ort.tgz '
      '"https://github.com/microsoft/onnxruntime/releases/download/v%s/onnxruntime-linux-x64-gpu-%s.tgz"'
      % (ORT_VERSION, ORT_VERSION)).returncode != 0:
    fail("ort_dl")
os.makedirs("%s/ort" % WORK, exist_ok=True)
sh('tar -C "%s/ort" --strip-components=1 -xzf /tmp/ort.tgz' % WORK)
libs = glob.glob("%s/ort/**/libonnxruntime.so*" % WORK, recursive=True)
if not libs:
    fail("ort_nolib")
ORT_LIB = libs[0]
ORT_DIR = os.path.dirname(ORT_LIB)

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

MODEL_DIR_ACTIVE = BGE
EMBED_ENV = {}
if PRECISION == "fp16":
    BGE16 = "%s/bge-m3-fp16" % WORK
    if sh("pip install --quiet onnx onnxruntime").returncode != 0:
        fail("fp16_deps")
    os.makedirs(BGE16, exist_ok=True)
    conv_py = "%s/convfp16.py" % WORK
    with open(conv_py, "w") as cf:
        cf.write(
            "import onnx\n"
            "from onnxruntime.transformers.onnx_model import OnnxModel\n"
            "m = onnx.load(%r)\n"
            "om = OnnxModel(m)\n"
            "om.convert_float_to_float16(keep_io_types=True)\n"
            "onnx.save(om.model, %r, save_as_external_data=True, all_tensors_to_one_file=True, location='model.onnx_data')\n"
            % ("%s/model.onnx" % BGE, "%s/model.onnx" % BGE16))
    if sh('python3 "%s"' % conv_py).returncode != 0 or not os.path.isfile("%s/model.onnx" % BGE16):
        fail("fp16_convert")
    shutil.copy("%s/tokenizer.json" % BGE, "%s/tokenizer.json" % BGE16)
    with open("%s/models.yaml" % WORK, "w") as mf:
        mf.write("active: bge-m3-fp16\nmodels:\n  - name: bge-m3-fp16\n    family: bge-m3\n"
                 "    dir: %s\n    precision: fp16\n    dim: 1024\n    normalization: l2\n"
                 "    revision: %s\n    tokenizer_revision: %s\n    tokenizer_dir: %s\n"
                 "    inputs: [input_ids, attention_mask]\n    output: sentence_embedding\n"
                 % (BGE16, BGE_COMMIT[:7], BGE_COMMIT[:7], BGE16))
    MODEL_DIR_ACTIVE = BGE16
    EMBED_ENV = {"EMBED_MODELS_CONFIG": "%s/models.yaml" % WORK, "EMBED_MODEL": "bge-m3-fp16"}
    say("fp16=ready")

# ---- build the embed binaries (sugarme + optional daulet/fasttok) ----------
env = dict(os.environ)
env["CGO_ENABLED"] = "1"
env["ONNXRUNTIME_SHARED_LIBRARY_PATH"] = ORT_LIB
env["LD_LIBRARY_PATH"] = ORT_DIR + ":" + env.get("LD_LIBRARY_PATH", "")
EMBED_SUGARME = "%s/embed-sugarme" % WORK
if sh('go build -tags onnx -o "%s" ./cmd/embed' % EMBED_SUGARME, env=env).returncode != 0:
    fail("build_sugarme")
say("build=ok bin=sugarme")

# fasttok = HuggingFace Rust tokenizer via daulet/tokenizers (CGO). Fetch the
# prebuilt static lib for this daulet version and link it. This is the candidate
# fix for the CPU-tokenise starvation; the A/B below measures its real uplift.
EMBED_DAULET = ""
DAULET_VER = os.environ.get("DAULET_VERSION", "v1.27.0")
libdir = "%s/libtok" % WORK
os.makedirs(libdir, exist_ok=True)
got_lib = False
for asset in ("libtokenizers.linux-amd64.tar.gz", "libtokenizers.linux-x86_64.tar.gz"):
    u = "https://github.com/daulet/tokenizers/releases/download/%s/%s" % (DAULET_VER, asset)
    if sh('curl -fsSL --retry 5 "%s" -o "%s/lib.tgz"' % (u, libdir)).returncode == 0:
        sh('tar -C "%s" -xzf "%s/lib.tgz"' % (libdir, libdir))
        if glob.glob("%s/**/libtokenizers.a" % libdir, recursive=True) or os.path.isfile("%s/libtokenizers.a" % libdir):
            got_lib = True
            break
if got_lib:
    libpath = (glob.glob("%s/**/libtokenizers.a" % libdir, recursive=True) + ["%s/libtokenizers.a" % libdir])[0]
    libloc = os.path.dirname(libpath)
    fenv = dict(env)
    fenv["CGO_LDFLAGS"] = "-L%s" % libloc
    EMBED_DAULET = "%s/embed-daulet" % WORK
    b = sh('go build -tags "onnx fasttok" -o "%s" ./cmd/embed' % EMBED_DAULET, env=fenv)
    if b.returncode != 0:
        say("build=fail bin=daulet detail=%s" % ((b.stderr or "")[-200:]).replace("\n", " "))
        EMBED_DAULET = ""
    else:
        say("build=ok bin=daulet ver=%s" % DAULET_VER)
else:
    say("daulet=skip detail=no_libtokenizers_asset")


def gpu_sampler(stop_evt, out):
    """Sample total GPU utilisation (both T4s) every 0.5s into out['samples']."""
    while not stop_evt.is_set():
        r = sh("nvidia-smi --query-gpu=utilization.gpu --format=csv,noheader,nounits")
        for ln in r.stdout.splitlines():
            ln = ln.strip()
            if ln.isdigit():
                out["samples"].append(int(ln))
        stop_evt.wait(0.5)


def run_config(name, binary, extra_env, sessions_per_gpu="1"):
    """Embed a FRESH copy of the base slice under one engine config; return metrics."""
    db = "%s/run-%s.duckdb" % (WORK, name)
    shutil.copy(BASE_DB, db)
    e = dict(env)
    e["EMBED_MODEL_DIR"] = MODEL_DIR_ACTIVE
    e["ORT_EP"] = EP
    e["EMBED_GRAPH_OPT"] = "1"
    e["EMBED_PROFILE"] = "1"
    e["EMBED_PROFILE_EVERY"] = "4"
    e["EMBED_SESSIONS_PER_GPU"] = sessions_per_gpu
    e.update(EMBED_ENV)
    e.update(extra_env)
    report = "%s/report-%s.json" % (WORK, name)
    cmd = ('"%s" --db "%s" --embed-floor "%s" --series "%s" --order recent --resume '
           '--limit "%s" --no-hnsw --require-semantic --report json --progress-every 200'
           % (binary, db, FLOOR, SERIES, LIMIT))
    stop = threading.Event()
    util = {"samples": []}
    sampler = threading.Thread(target=gpu_sampler, args=(stop, util), daemon=True)
    sampler.start()
    t0 = time.time()
    last_profile = ""
    with open(report, "w") as out, open("/tmp/err-%s" % name, "w") as errf:
        proc = subprocess.Popen(cmd, shell=True, env=e, stdout=out, stderr=subprocess.PIPE, text=True, bufsize=1)
        for line in proc.stderr:
            errf.write(line)
            s = line.rstrip()
            if "embed-profile:" in s:
                last_profile = s
            if "progress" in s or "profile" in s:
                sys.stdout.write("%s| %s\n" % (name, s[-170:]))
                sys.stdout.flush()
        rc = proc.wait()
    elapsed = time.time() - t0
    stop.set()
    sampler.join(timeout=2)
    rep = {}
    try:
        rep = json.load(open(report))
    except Exception:
        pass
    emb = int(rep.get("embedded_clauses", 0) or 0)
    s = util["samples"] or [0]
    util_mean = sum(s) / len(s)
    util_max = max(s)
    cls = (emb / elapsed) if elapsed > 0 else 0.0
    say("cfg=%s rc=%d embedded=%d elapsed=%.0fs cl_s=%.2f gpu_util_mean=%.0f%% gpu_util_max=%d%% nsamp=%d"
        % (name, rc, emb, elapsed, cls, util_mean, util_max, len(s)))
    if last_profile:
        say("cfg=%s %s" % (name, last_profile.split("embed-profile:")[-1].strip()))
    return {"name": name, "cl_s": cls, "embedded": emb, "elapsed": elapsed,
            "util_mean": util_mean, "util_max": util_max, "rc": rc}


# ---- A/B: daulet confirmed the fix (7×, GPU-bound). Now isolate the batching +
# warmup levers IN the GPU-bound regime, where they should finally matter. ----
results = []
# Reference: pure-Go baseline (~6 cl/s, GPU starved).
results.append(run_config("sugarme_base", EMBED_SUGARME, {"EMBED_SHAPE_LADDER": "0", "EMBED_TOKEN_BUDGET_BATCHING": "0"}))
if EMBED_DAULET:
    # daulet, all GPU levers OFF — the raw tokenizer win alone.
    results.append(run_config("daulet_plain", EMBED_DAULET,
                              {"EMBED_SHAPE_LADDER": "0", "EMBED_TOKEN_BUDGET_BATCHING": "0", "EMBED_WARMUP": "0"}))
    # daulet + ladder + token-budget (no warmup).
    results.append(run_config("daulet_levers", EMBED_DAULET, {"EMBED_WARMUP": "0"}))
    # daulet + ladder + token-budget + shape warmup (the full engine).
    results.append(run_config("daulet_full", EMBED_DAULET, {}))

base = results[0]["cl_s"] or 1e-9
for r in results:
    say("SUMMARY cfg=%-18s cl_s=%7.2f speedup=%5.2fx util_mean=%3.0f%% util_max=%d%%"
        % (r["name"], r["cl_s"], r["cl_s"] / base, r["util_mean"], r["util_max"]))
say("step=OK complete=1")
