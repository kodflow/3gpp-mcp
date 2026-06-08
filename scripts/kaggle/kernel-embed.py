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
import warnings

# Silence Python 3.12 SyntaxWarnings emitted by Kaggle's notebook→HTML conversion
# (mistune / nbconvert escape-sequence deprecations like '\|' and '\_'). They are
# pure platform noise — unrelated to the embed — but they alarm a log reader. Best
# effort: set it both via the filter and the env so a child interpreter inherits it.
warnings.filterwarnings("ignore")
os.environ.setdefault("PYTHONWARNINGS", "ignore")

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
# The lexical base is pulled from the PRIVATE GHCR image, not the (retired) public
# Release. OWNER namespaces the registry path; GHCR_PAT is a read:packages token the
# driver injects (the kernel is is_private:true). CRANE_VER pins the puller.
OWNER = os.environ.get("GHCR_OWNER", "kodflow")
GHCR_PAT = os.environ.get("GHCR_PAT", "").strip()
CRANE_VER = os.environ.get("CRANE_VER", "v0.20.2")
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
# Per-RELEASE-lot mode (kaggle-embed-lots.sh): when EMBED_RELEASES is a non-empty
# comma-separated label set, this kernel shards by an explicit RELEASE SET instead of
# by SERIES, so two balanced lots can embed concurrently in two sessions. LOT names
# the shard ("A"/"B") for the resume Dataset + RESULT markers. The slice, the embed
# selector, and the resume/state slug all key on SHARD, which is "lot<LOT>" in lot mode
# and "s<SERIES>" otherwise — the proven per-series path is byte-for-byte unchanged.
RELEASES = os.environ.get("EMBED_RELEASES", "").strip()
LOT = os.environ.get("LOT", "").strip()
SHARD = ("lot%s" % LOT) if RELEASES else ("s%s" % SERIES)
os.chdir(WORK)
os.environ.pop("EMBEDDER", None)  # force the real onnx/CUDA backend (never Local)

say("step=start floor=%s shard=%s series=%s releases=%s precision=%s ort=%s branch=%s budget=%ds"
    % (FLOOR, SHARD, SERIES, (RELEASES or "-"), PRECISION, ORT_VERSION, BRANCH, TIME_BUDGET))

# GPU-optional: CUDA when a GPU is attached (T4 Tensor-Core fp16 path), else CPU so
# the pipeline + download path are still exercised end-to-end.
g = sh("nvidia-smi -L")
if g.returncode == 0 and g.stdout.strip():
    EP = "cuda"
    # Kaggle's "T4" accelerator is 2×T4. The Go embedder auto-detects the GPU count
    # (same `nvidia-smi -L`) and runs one ORT session per device, so both are used.
    gpus = [l for l in g.stdout.splitlines() if l.strip().startswith("GPU ")]
    say("gpu=present count=%d detail=%s ep=cuda" % (len(gpus), gpus[0].strip() if gpus else "?"))
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
    "RESUME_DB", "/kaggle/input/3gpp-embedded-%s/3gpp-embedded.duckdb" % SHARD)
# Kaggle's exact dataset mount dir is brittle (slug/case/version), so an explicit
# RESUME_DB path can miss a dataset that IS attached → silent fresh-slice (re-embeds
# everything). If the explicit path isn't a file, GLOB for the resume DB anywhere under
# /kaggle/input/ and use it — robust to whatever dir Kaggle actually mounted it at.
if not os.path.isfile(RESUME_DB):
    _hits = sorted(glob.glob("/kaggle/input/**/3gpp-embedded.duckdb", recursive=True))
    if _hits:
        say("resume=glob_found path=%s (env RESUME_DB=%s missed)" % (_hits[0], RESUME_DB))
        RESUME_DB = _hits[0]
# ALWAYS fresh-slice from the CURRENT private base, then carry over only the vectors
# whose clause is byte-identical. A re-published base reshuffles chunk_ids for CHANGED
# series, so copying a stale resume DB verbatim (the old behaviour) would embed old
# clauses and MISALIGN on overlay (overlay matches by chunk_id). Instead the slice
# carries the base's current chunk_ids, and the carry-over reuses a prior vector only
# when (spec_id, release, clause_path, text) match exactly — so unchanged clauses keep
# their vector (no GPU) while changed/new ones stay NULL and get re-embedded. This makes
# the loop self-correcting: a stale resume DB can never poison a re-published series.
RESUME_PRESENT = os.path.isfile(RESUME_DB)
say("resume=%s src=%s" % ("present" if RESUME_PRESENT else "absent",
                          RESUME_DB if RESUME_PRESENT else "-"))
# The lexical base lives ONLY in the PRIVATE GHCR image ghcr.io/<owner>/3gpp-corpus:latest
# (FROM scratch + /3gpp.duckdb) — the public `latest` Release was retired by the GHCR
# migration. Pull it with a read-scoped token: authenticate with crane, then
# `crane export` flattens the scratch image's filesystem to a tar from which we extract
# only the DB. The PAT is fed on STDIN (never on argv) so it cannot reach a captured log.
if not GHCR_PAT:
    fail("ghcr_pat_missing",
         "GHCR_PAT absent from kernel env — the driver must inject a read:packages token")
if sh('curl -fsSL --retry 5 -o /tmp/crane.tgz '
      '"https://github.com/google/go-containerregistry/releases/download/%s/'
      'go-containerregistry_Linux_x86_64.tar.gz"' % CRANE_VER).returncode != 0:
    fail("crane_dl")
if sh('tar -xzf /tmp/crane.tgz -C /tmp crane').returncode != 0:
    fail("crane_untar")
login = sh('printf %%s "$GHCR_PAT" | /tmp/crane auth login ghcr.io -u "%s" --password-stdin'
           % OWNER)
if login.returncode != 0:
    fail("ghcr_login", (login.stderr or "").strip()[-160:])
img = "ghcr.io/%s/3gpp-corpus:latest" % OWNER
if sh('/tmp/crane export "%s" - | tar -xC "%s" 3gpp.duckdb' % (img, WORK)).returncode != 0:
    fail("ghcr_export", img)
if not os.path.isfile("%s/3gpp.duckdb" % WORK):
    fail("ghcr_no_db", img)
os.replace("%s/3gpp.duckdb" % WORK, "%s/full.duckdb" % WORK)
fulln = duckdb_scalar("%s/full.duckdb" % WORK, "SELECT count(*) FROM clauses;")
say("full_clauses=%s" % fulln)
# Emit the WHOLE-corpus per-release clause counts once (we have full.duckdb here,
# on Kaggle's fast network). The local dashboard reads this as its denominator,
# so it never has to download the 7GB corpus on a flaky laptop link.
rt = sh('duckdb "%s/full.duckdb" -noheader -list '
        '"SELECT release || \'=\' || count(*) FROM clauses GROUP BY release;"' % WORK)
if rt.returncode == 0 and rt.stdout.strip():
    pairs = ",".join(l.strip() for l in rt.stdout.splitlines() if l.strip())
    say("release_totals=%s" % pairs)
# Lot mode slices by an explicit RELEASE SET; series mode by the 2-digit series.
# The release labels are validated against the safe Rel-NN / phase grammar before
# interpolation (they originate from the operator's lot definition, never the DB).
if RELEASES:
    rels = [r.strip() for r in RELEASES.split(",") if r.strip()]
    for r in rels:
        if not re.match(r"^[A-Za-z0-9._-]+$", r):
            fail("bad_release", "release=%s" % r)
    where = "release IN (%s)" % ",".join("'%s'" % r for r in rels)
else:
    where = "substr(spec_id,1,2)='%s'" % SERIES
slice_sql = ("ATTACH '%s/full.duckdb' AS s (READ_ONLY); "
             "CREATE TABLE clauses AS SELECT * FROM s.clauses "
             "WHERE %s;" % (WORK, where))
if sh('duckdb "%s" "%s"' % (EMBEDDED_DB, slice_sql)).returncode != 0:
    fail("slice")
sln = duckdb_scalar(EMBEDDED_DB, "SELECT count(*) FROM clauses;")
say("sliced_clauses=%s" % sln)
if not (sln.isdigit() and int(sln) >= 1):
    fail("empty_slice", "shard=%s full=%s" % (SHARD, fulln))
# Carry over prior vectors from the resume DB onto the FRESH slice, keyed by the
# clause's natural identity + exact text (NOT chunk_id, which is unstable across a
# re-published base). Unchanged clauses recover their vector → no GPU; changed/new
# clauses stay NULL → re-embedded. Non-fatal: a carry failure just means re-embed.
if RESUME_PRESENT:
    carry_sql = (
        "ATTACH '%s' AS r (READ_ONLY); "
        "UPDATE clauses SET embedding = rc.embedding, embedding_hash = rc.embedding_hash "
        "FROM r.clauses rc "
        "WHERE clauses.spec_id = rc.spec_id AND clauses.release = rc.release "
        "AND clauses.clause_path = rc.clause_path AND clauses.text = rc.text "
        "AND rc.embedding IS NOT NULL;" % RESUME_DB)
    cr = sh('duckdb "%s" "%s"' % (EMBEDDED_DB, carry_sql))
    if cr.returncode != 0:
        say("resume_carry=failed detail=%s (continuing as fresh re-embed)"
            % (cr.stderr or "").strip()[-120:])
    else:
        carried = duckdb_scalar(
            EMBEDDED_DB, "SELECT count(*) FROM clauses WHERE embedding IS NOT NULL;")
        say("resume_carry=ok reused_vectors=%s of_sliced=%s" % (carried or "?", sln))
# DISK: full.duckdb (~1.7GB) is consumed — drop it so /kaggle/working doesn't carry
# it alongside the growing embedded DB + models (the disk-exhaustion crash root cause).
try:
    os.remove("%s/full.duckdb" % WORK)
    say("cleanup=full.duckdb_removed")
except OSError:
    pass

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
    # ONNX Runtime's OWN transformer fp16 converter — NOT onnxconverter_common.
    # The latter blocks ops like CumSum/Range (XLM-RoBERTa position-ids, which MUST
    # stay fp32: a >2048-token cumsum is inexact in fp16) but, under the
    # disable_shape_infer we need for a >2GB model, omits the Cast back to fp32 at
    # those boundaries — ORT then rejects the graph (Type float16 vs float at
    # /0/auto_model/Cast). onnxruntime.transformers.OnnxModel is Microsoft's BERT/
    # RoBERTa fp16 tool: symbolic shape inference (handles >2GB) + a transformer-tuned
    # block list + CORRECT boundary Cast insertion. keep_io_types keeps the fp32 IO
    # so the Go side still feeds int64 ids/mask and reads a float32 sentence_embedding.
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
            "print('fp16_saved')\n"
            % ("%s/model.onnx" % BGE, "%s/model.onnx" % BGE16)
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
    # DISK: the fp32 source model (~2.3GB) is no longer needed once fp16 is built — drop it
    # so it doesn't sit next to the fp16 model + the growing embedded DB (crash root cause).
    shutil.rmtree(BGE, ignore_errors=True)
    say("cleanup=fp32_model_removed")

# ---- build the onnx embed binary -------------------------------------------
# fasttok = HuggingFace Rust tokenizer (daulet/tokenizers, CGO). The 2×T4 probe
# proved it: the pure-Go sugarme path leaves the GPU starved (util 3-5%, ~6 cl/s,
# TOKENIZE-bound); daulet drops tokenise prep from ~65s to ~1s and lifts throughput
# ~7× (≈43-56 cl/s, now GPU/RUN-bound). So we fetch the prebuilt static lib and build
# -tags "onnx fasttok" by default; if the lib is unavailable we degrade to the pure-Go
# build (slower but still correct — never block).
env = dict(os.environ)
env["CGO_ENABLED"] = "1"
env["ONNXRUNTIME_SHARED_LIBRARY_PATH"] = ORT_LIB
env["LD_LIBRARY_PATH"] = ORT_DIR + ":" + env.get("LD_LIBRARY_PATH", "")
BUILD_TAGS = "onnx"
DAULET_VER = os.environ.get("DAULET_VERSION", "v1.27.0")
FASTTOK = os.environ.get("EMBED_FASTTOK", "1") != "0"
if FASTTOK:
    libdir = "%s/libtok" % WORK
    os.makedirs(libdir, exist_ok=True)
    got = False
    for asset in ("libtokenizers.linux-amd64.tar.gz", "libtokenizers.linux-x86_64.tar.gz"):
        u = "https://github.com/daulet/tokenizers/releases/download/%s/%s" % (DAULET_VER, asset)
        if sh('curl -fsSL --retry 5 "%s" -o "%s/lib.tgz"' % (u, libdir)).returncode == 0:
            sh('tar -C "%s" -xzf "%s/lib.tgz"' % (libdir, libdir))
            hits = glob.glob("%s/**/libtokenizers.a" % libdir, recursive=True) + ["%s/libtokenizers.a" % libdir]
            hits = [h for h in hits if os.path.isfile(h)]
            if hits:
                env["CGO_LDFLAGS"] = "-L%s" % os.path.dirname(hits[0])
                BUILD_TAGS = "onnx fasttok"
                got = True
                break
    say("fasttok=%s ver=%s" % ("on" if got else "off_no_lib", DAULET_VER))
build = sh('go build -tags "%s" -o "%s/embed" ./cmd/embed' % (BUILD_TAGS, WORK), env=env)
if build.returncode != 0:
    # If the fasttok link failed, fall back to the pure-Go build so the run still
    # completes (correct, just slower) instead of failing the whole kernel.
    if "fasttok" in BUILD_TAGS:
        say("build=retry detail=fasttok_link_failed → pure-go")
        env.pop("CGO_LDFLAGS", None)
        build = sh('go build -tags onnx -o "%s/embed" ./cmd/embed' % WORK, env=env)
        BUILD_TAGS = "onnx"
    if build.returncode != 0:
        fail("build", (build.stderr or "").replace("\n", " ")[-200:])
say("build=ok tags=%s" % BUILD_TAGS)

# ---- EMBED (recent-first, resumable, time-bounded) -------------------------
env["EMBED_MODEL_DIR"] = MODEL_DIR_ACTIVE
env["ORT_EP"] = EP
env["EMBED_GRAPH_OPT"] = "1"
env.update(EMBED_ENV)  # fp16: EMBED_MODELS_CONFIG + EMBED_MODEL=bge-m3-fp16
REPORT = "%s/embed-report.json" % WORK
# Lot mode selects the work-list by the SAME release SET that sliced the DB (belt &
# braces: the slice already restricts the table, --embed-releases re-asserts it on the
# work-list query); series mode keeps the per-series selector. Selection-only — neither
# changes EmbedIdentity, so fp16 vectors are bit-for-bit what the per-series path makes.
selector = ('--embed-releases "%s"' % RELEASES) if RELEASES else ('--series "%s"' % SERIES)
cmd = ('"%s/embed" --db "%s" --embed-floor "%s" %s '
       "--order recent --resume --checkpoint-every \"%s\" --no-hnsw "
       "--require-semantic --report json"
       % (WORK, EMBEDDED_DB, FLOOR, selector, CHECKPOINT_EVERY))
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
EMBED_STATE_DS = os.environ.get("EMBED_STATE_DS") or (("%s/3gpp-embedded-%s" % (KU, SHARD)) if KU else "")
if KU and KK and EMB >= 1:
    if have("kaggle") or sh("pip install --quiet kaggle").returncode == 0:
        out = "%s/state" % WORK
        os.makedirs(out, exist_ok=True)
        shutil.copy(EMBEDDED_DB, "%s/3gpp-embedded.duckdb" % out)
        json.dump({"title": "3gpp-embedded-%s" % SHARD, "id": EMBED_STATE_DS,
                   "licenses": [{"name": "CC0-1.0"}]},
                  open("%s/dataset-metadata.json" % out, "w"))
        if sh('kaggle datasets version -p "%s" -m "shard=%s embedded=%d" --dir-mode zip'
              % (out, SHARD, EMB)).returncode == 0:
            say("versioned=%s" % EMBED_STATE_DS)
        elif sh('kaggle datasets create -p "%s" --dir-mode zip' % out).returncode == 0:
            say("created=%s" % EMBED_STATE_DS)
        else:
            say("version=skip detail=version_and_create_failed")
    else:
        say("version=skip detail=no_kaggle_cli")
else:
    say("version=skip detail=no_creds_or_nothing_embedded embedded=%d" % EMB)

# ---- shrink the kernel output so the driver's `kernels output` pull is FAST -----
# Kaggle auto-saves ALL of /kaggle/working as the kernel output, and the driver
# downloads it to publish the resume dataset. Without this, every pull drags the
# multi-GB scratch (the 2GB BGE model, the ~7GB sliced corpus, ORT, the build tree)
# and hangs for many minutes. The driver only needs 3gpp-embedded.duckdb (+ the
# RESULT/report markers), so delete the scratch and keep just those.
KEEP = {"3gpp-embedded.duckdb", "RESULT.txt", "embed-report.json"}
try:
    os.chdir(WORK)  # never sit inside a dir we are about to remove
    for name in os.listdir(WORK):
        if name in KEEP:
            continue
        p = os.path.join(WORK, name)
        try:
            shutil.rmtree(p) if os.path.isdir(p) else os.remove(p)
        except Exception:
            pass
    say("cleanup=ok kept=%s" % ",".join(sorted(KEEP)))
except Exception as e:
    say("cleanup=skip detail=%s" % str(e)[:80])

# A complete series ends with null_at_floor==0 AND no timeout. A timeout/partial is a
# SUCCESSFUL increment — the driver re-launches to continue.
if rc == 0 and str(NUL) == "0":
    say("step=OK complete=1")
else:
    say("step=OK complete=0 (partial — resume next run)")
