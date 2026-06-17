# Kaggle GPU kernel for the RUST dense-embed campaign — replaces the Go embed path
# (user mandate: "remplace tout le go par du rust pour l'embed"). NATIVE PYTHON
# (Kaggle accepts only python/r/rmarkdown). ZERO-UPLOAD + RESUMABLE + self-diagnosing:
# everything is pulled from public/GHCR sources on the kernel (Internet on). Writes
# compact single-line "RESULT ..." markers to /kaggle/working/RESULT.txt.
#
# Pipeline (one shard = one series OR an explicit release set):
#   1. pull the lexical base DuckDB from the PRIVATE GHCR 3gpp-corpus image (crane),
#      slice it to this shard (DuckDB CLI).
#   2. download BGE-M3 (model.onnx + model.onnx_data + tokenizer.json) from HF.
#   3. build the Go bridge (cmd/embed-io) and the Rust embedder (cargo build --release,
#      ort with the CUDA EP → uses the Kaggle T4 GPU).
#   4. embed-io --export-worklist  →  embedder (GPU, progress bar, resume ledger)  →
#      embed-io --import-vectors --build-hnsw.
#   5. version the embedded DB + the vecs.jsonl ledger back to the resume Dataset.
#
# RESUME (load-bearing for the 12h cap): the shard keeps ONE output Kaggle Dataset
# ${KAGGLE_USERNAME}/3gpp-rust-embedded-<shard> holding 3gpp-embedded.duckdb AND
# vecs.jsonl. On START both are restored if mounted; vecs.jsonl is the Rust embedder's
# micro-resume ledger (already-embedded chunk_ids are skipped, new ones appended), so a
# killed session resumes from the last written vector, never from zero. END: version
# the partial DB + ledger back (driver from the laptop, or this tail with Kaggle Secrets).
#
# Config comes from os.environ — the driver PREPENDS os.environ.setdefault(...) lines.

import glob
import json
import os
import re
import shutil
import subprocess
import time

os.environ.setdefault("PYTHONWARNINGS", "ignore")

WORK = "/kaggle/working"
RESULT = os.path.join(WORK, "RESULT.txt")


def say(msg):
    line = "RESULT %s" % msg
    print(line, flush=True)
    try:
        with open(RESULT, "a") as f:
            f.write(line + "\n")
    except Exception:
        pass


def fail(code, detail=""):
    say("step=FAIL code=%s detail=%s" % (code, detail[:200]))
    raise SystemExit(1)


def sh(cmd, env=None, timeout=None):
    return subprocess.run(cmd, shell=True, text=True, capture_output=True, env=env, timeout=timeout)


def have(cmd):
    return shutil.which(cmd) is not None


def duckdb_scalar(db, query):
    r = sh('duckdb "%s" -noheader -list "%s"' % (db, query))
    return r.stdout.strip() if r.returncode == 0 else ""


# ---- config ----------------------------------------------------------------
OWNER = os.environ.get("GHCR_OWNER", "kodflow")
GHCR_PAT = os.environ.get("GHCR_PAT", "").strip()
CRANE_VER = os.environ.get("CRANE_VER", "v0.20.2")
BRANCH = os.environ.get("BRANCH", "main")
REPO = os.environ.get("REPO", "https://github.com/kodflow/3gpp-mcp")
SERIES = os.environ.get("SERIES", "21")
RELEASES = os.environ.get("EMBED_RELEASES", "").strip()
LOT = os.environ.get("LOT", "").strip()
BATCH = os.environ.get("EMBED_BATCH", "64")
LIMIT = os.environ.get("EMBED_LIMIT", "0")  # cap clauses this session (0 = all); for fast diag runs
# BGE-M3 ONNX on HF (external-data export). Pinned commit for reproducibility.
HF = os.environ.get(
    "BGE_HF",
    "https://huggingface.co/BAAI/bge-m3/resolve/main",
)
# Stop ~10.8h in so the version/validate tail always runs before Kaggle's 12h kill.
TIME_BUDGET = int(os.environ.get("EMBED_TIME_BUDGET", "39000"))

if not re.match(r"^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$", OWNER):
    fail("bad_owner", "GHCR_OWNER=%s" % OWNER)
if not re.match(r"^\d{2}$", SERIES):
    fail("bad_series", "SERIES=%s" % SERIES)
if not re.match(r"^[A-Za-z0-9]{0,8}$", LOT):
    fail("bad_lot", "LOT=%s" % LOT)
if not re.match(r"^\d{1,4}$", BATCH):
    fail("bad_batch", "EMBED_BATCH=%s" % BATCH)
if not re.match(r"^\d{1,8}$", LIMIT):
    fail("bad_limit", "EMBED_LIMIT=%s" % LIMIT)

SHARD = ("lot%s" % LOT) if (RELEASES and LOT) else ("s%s" % SERIES)
os.chdir(WORK)
say("step=start shard=%s series=%s releases=%s batch=%s branch=%s budget=%ds"
    % (SHARD, SERIES, (RELEASES or "-"), BATCH, BRANCH, TIME_BUDGET))

# GPU-optional: CUDA when a GPU is attached (the ort CUDA EP engages), else CPU.
g = sh("nvidia-smi -L")
if g.returncode == 0 and g.stdout.strip():
    say("gpu=present detail=%s" % g.stdout.splitlines()[0].strip())
else:
    say("gpu=absent (CPU fallback — ort uses the CPU EP)")

sh("apt-get update -qq && apt-get install -y -qq zstd unzip")

# ---- Go toolchain (for cmd/embed-io) ---------------------------------------
GO_FALLBACK = "1.26.3"
_gomod = sh('curl -fsSL --retry 5 "%s/raw/%s/go.mod"' % (REPO, BRANCH)).stdout
_m = re.search(r"(?m)^go[ \t]+([0-9]+\.[0-9]+\.[0-9]+)", _gomod)
GO_VER = _m.group(1) if _m else GO_FALLBACK
if sh("curl -fsSL --retry 5 https://go.dev/dl/go%s.linux-amd64.tar.gz -o /tmp/go.tgz" % GO_VER).returncode != 0:
    fail("go_dl", "version=%s" % GO_VER)
sh("rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz")
os.environ["PATH"] = "/usr/local/go/bin:" + os.environ.get("PATH", "")
os.environ["GOTOOLCHAIN"] = "local"
say("go=%s" % (sh("go version").stdout.strip()[:40]))

# ---- Rust toolchain (for the embedder crate) -------------------------------
if not have("cargo"):
    sh("curl -fsSL https://sh.rustup.rs | sh -s -- -y --default-toolchain stable --profile minimal")
os.environ["PATH"] = os.path.expanduser("~/.cargo/bin") + ":" + os.environ.get("PATH", "")
if not have("cargo"):
    fail("no_cargo")
say("cargo=%s" % (sh("cargo --version").stdout.strip()[:40]))

# ---- duckdb CLI ------------------------------------------------------------
if sh("curl -fsSL --retry 5 -o /tmp/duckdb.zip "
      "https://github.com/duckdb/duckdb/releases/download/v1.1.3/duckdb_cli-linux-amd64.zip").returncode != 0:
    fail("duckdb_dl")
os.makedirs("/tmp/bin", exist_ok=True)
sh("cd /tmp/bin && unzip -o -q /tmp/duckdb.zip")
os.environ["PATH"] = "/tmp/bin:" + os.environ.get("PATH", "")
if sh("duckdb --version").returncode != 0:
    fail("duckdb_run")

# ---- clone the repo --------------------------------------------------------
src = os.path.join(WORK, "src")
if sh('git clone --depth 1 -b "%s" "%s" "%s"' % (BRANCH, REPO, src)).returncode != 0:
    if sh('curl -fsSL --retry 5 -o /tmp/src.tgz "%s/archive/refs/heads/%s.tar.gz"' % (REPO, BRANCH)).returncode == 0:
        os.makedirs(src, exist_ok=True)
        sh('tar -C "%s" --strip-components=1 -xzf /tmp/src.tgz' % src)
    if not os.path.isdir(os.path.join(src, "cmd")):
        fail("clone")
os.chdir(src)

# ---- pull the lexical base + slice this shard ------------------------------
if not GHCR_PAT:
    fail("ghcr_pat_missing", "driver must inject a read:packages token")
if sh('curl -fsSL --retry 5 -o /tmp/crane.tgz '
      '"https://github.com/google/go-containerregistry/releases/download/%s/'
      'go-containerregistry_Linux_x86_64.tar.gz"' % CRANE_VER).returncode != 0:
    fail("crane_dl")
sh("tar -xzf /tmp/crane.tgz -C /tmp crane")
if sh('printf %%s "$GHCR_PAT" | /tmp/crane auth login ghcr.io -u "%s" --password-stdin' % OWNER,
      env={**os.environ, "GHCR_PAT": GHCR_PAT}).returncode != 0:
    fail("ghcr_login")
img = "ghcr.io/%s/3gpp-corpus:latest" % OWNER
if sh('/tmp/crane export "%s" - | tar -xC "%s" 3gpp.duckdb' % (img, WORK)).returncode != 0:
    fail("ghcr_export", img)
os.replace("%s/3gpp.duckdb" % WORK, "%s/full.duckdb" % WORK)
say("full_clauses=%s" % duckdb_scalar("%s/full.duckdb" % WORK, "SELECT count(*) FROM clauses;"))

EMBEDDED_DB = os.path.join(WORK, "3gpp-embedded.duckdb")
VECS = os.path.join(WORK, "vecs.jsonl")
# RESUME: restore a prior embedded DB + the vecs.jsonl ledger if a dataset is mounted.
for pat, dest in (("**/3gpp-embedded.duckdb", EMBEDDED_DB), ("**/vecs.jsonl", VECS)):
    if not os.path.isfile(dest):
        hits = sorted(glob.glob("/kaggle/input/%s" % pat, recursive=True))
        if hits:
            shutil.copy(hits[0], dest)
            say("resume=restored %s" % os.path.basename(dest))

# Build the shard slice (fresh from the current base; the vecs.jsonl ledger carries the
# embed resume, keyed by chunk_id — stable within a base version).
if RELEASES:
    rels = [r.strip() for r in RELEASES.split(",") if r.strip()]
    for r in rels:
        if not re.match(r"^[A-Za-z0-9._-]+$", r):
            fail("bad_release", r)
    where = "release IN (%s)" % ",".join("'%s'" % r for r in rels)
else:
    where = "substr(spec_id,1,2)='%s'" % SERIES
if not os.path.isfile(EMBEDDED_DB):
    slice_sql = ("ATTACH '%s/full.duckdb' AS s (READ_ONLY); "
                 "CREATE TABLE clauses AS SELECT * FROM s.clauses WHERE %s;" % (WORK, where))
    if sh('duckdb "%s" "%s"' % (EMBEDDED_DB, slice_sql)).returncode != 0:
        fail("slice")
sln = duckdb_scalar(EMBEDDED_DB, "SELECT count(*) FROM clauses;")
say("sliced_clauses=%s" % sln)
if not (sln.isdigit() and int(sln) >= 1):
    fail("empty_slice", "shard=%s" % SHARD)

# ---- BGE-M3 model files (external-data ONNX + tokenizer) -------------------
BGE = os.path.join(WORK, "bge-m3")
os.makedirs(BGE, exist_ok=True)
for url, dest in (
    ("%s/onnx/model.onnx" % HF, "%s/model.onnx" % BGE),
    ("%s/onnx/model.onnx_data" % HF, "%s/model.onnx_data" % BGE),
    ("%s/tokenizer.json" % HF, "%s/tokenizer.json" % BGE),
):
    if not os.path.isfile(dest):
        if sh('curl -fSL --retry 5 -C - -A "Mozilla/5.0" "%s" -o "%s"' % (url, dest)).returncode != 0:
            fail("model_dl", url)
say("model_data_bytes=%d" % (os.path.getsize("%s/model.onnx_data" % BGE)))

# ---- fp16 conversion (keep_io_types) — kernel-identical to corpus-data-image.yml.
# fp16 is ~2-6x faster on T4 Tensor Cores AND halves attention memory; the served data
# image bakes fp16, so the campaign MUST be fp16 for the EmbedIdentity to match (else
# semantic is refused at serve). keep_io_types keeps fp32 IO so the Rust embedder reads
# []f32 unchanged — only the weights become fp16.
BGE16 = os.path.join(WORK, "bge-m3-fp16")
os.makedirs(BGE16, exist_ok=True)
sh("python3 -m pip install --quiet onnx onnxruntime sympy packaging")
conv = os.path.join(WORK, "convfp16.py")
with open(conv, "w") as f:
    f.write(
        "import onnx\n"
        "from onnxruntime.transformers.onnx_model import OnnxModel\n"
        "m = onnx.load('%s/model.onnx')\n"
        "om = OnnxModel(m); om.convert_float_to_float16(keep_io_types=True)\n"
        "onnx.save(om.model, '%s/model.onnx', save_as_external_data=True, "
        "all_tensors_to_one_file=True, location='model.onnx_data')\n"
        "print('fp16_saved')\n" % (BGE, BGE16)
    )
if sh("python3 %s" % conv).returncode != 0 or not os.path.isfile("%s/model.onnx_data" % BGE16):
    fail("fp16_convert")
sh("cp '%s/tokenizer.json' '%s/'" % (BGE, BGE16))
# The fp16 model registry — fields MUST match corpus-data-image.yml's models.yaml so
# cmd/embedid emits the SAME identity the served fp16 image expects (dir is irrelevant
# to the identity — only family/precision/dim/normalization/revision are).
MODELS_YAML = os.path.join(WORK, "models.yaml")
with open(MODELS_YAML, "w") as f:
    f.write(
        "active: bge-m3-fp16\n"
        "models:\n"
        "  - name: bge-m3-fp16\n"
        "    family: bge-m3\n"
        "    dir: %s\n"
        "    precision: fp16\n"
        "    dim: 1024\n"
        "    normalization: l2\n"
        "    revision: 5617a9f\n"
        "    tokenizer_revision: 5617a9f\n"
        "    tokenizer_dir: %s\n"
        "    inputs: [input_ids, attention_mask]\n"
        "    output: sentence_embedding\n" % (BGE16, BGE16)
    )
EMBED_MODEL_ENV = {**os.environ, "EMBED_MODELS_CONFIG": MODELS_YAML}

# ---- build the Go bridge + the Rust embedder -------------------------------
if sh("CGO_ENABLED=1 go build -o /tmp/embed-io ./cmd/embed-io").returncode != 0:
    fail("build_embed_io")
# fp16 identity (EMBED_MODELS_CONFIG → cmd/embedid resolves the fp16 model).
ID = sh("CGO_ENABLED=1 go run ./cmd/embedid", env=EMBED_MODEL_ENV).stdout.strip()
if not re.match(r"^[0-9a-f]{6,}$", ID):
    fail("bad_identity", ID)
say("embed_identity=%s (fp16)" % ID)
bc = sh("cargo build --release --manifest-path rust/embedder/Cargo.toml")
if bc.returncode != 0:
    fail("cargo_build", (bc.stderr or "")[-200:])
EMBEDDER = os.path.join(src, "rust/embedder/target/release/embedder")
# ort's `download-binaries` fetches libonnxruntime.so at BUILD time into the ort-sys
# OUT_DIR, but the release binary has no $ORIGIN RPATH, so at RUNTIME it can't find the
# lib (error 127: "libonnxruntime.so: cannot open shared object file"). Locate the .so
# anywhere under target/ and prepend its dir to LD_LIBRARY_PATH for the embedder run.
_ortlibs = sorted(
    glob.glob(os.path.join(src, "rust/embedder/target/release/**/libonnxruntime.so*"), recursive=True),
    key=len,
)
if _ortlibs:
    _libdir = os.path.dirname(_ortlibs[0])
    os.environ["LD_LIBRARY_PATH"] = _libdir + ":" + os.environ.get("LD_LIBRARY_PATH", "")
    say("ort_lib=%s" % _ortlibs[0])
else:
    say("ort_lib=NONE (libonnxruntime.so not found under target/ — embed will fail)")

# ---- export → embed → import ----------------------------------------------
WL = os.path.join(WORK, "work.jsonl")
if sh('/tmp/embed-io --db "%s" --export-worklist "%s" --resume' % (EMBEDDED_DB, WL)).returncode != 0:
    fail("export_worklist")
say("worklist_lines=%s" % (sh('wc -l < "%s"' % WL).stdout.strip()))

# Run the embedder with output INHERITED (not captured) so its PROGRESS lines stream
# LIVE into the Kaggle log — capturing them (the old sh()) buffered everything until the
# end, hiding the progress bar. The embedder prints "PROGRESS done/total (%) rate eta"
# every 2000 clauses.
embcmd = '"%s" --in "%s" --out "%s" --model-dir "%s" --embed-identity "%s" --batch %s --limit %s --require-cuda' % (
    EMBEDDER, WL, VECS, BGE16, ID, BATCH, LIMIT)
try:
    emb_rc = subprocess.run(embcmd, shell=True, env=os.environ, timeout=TIME_BUDGET).returncode
except subprocess.TimeoutExpired:
    emb_rc = 124
    say("embedder hit TIME_BUDGET — partial (ledger resumes next run)")
if emb_rc != 0:
    # Non-fatal: the vecs.jsonl ledger is a valid resume point; import what we have. The
    # actual error already streamed live above.
    say("embedder_rc=%d (partial allowed — ledger resumes next run; see live log above)" % emb_rc)
vec_lines = sh('wc -l < "%s"' % VECS).stdout.strip() if os.path.isfile(VECS) else "0"
say("vectors_written=%s" % vec_lines)

if sh('/tmp/embed-io --db "%s" --import-vectors "%s" --embed-identity "%s" --build-hnsw'
      % (EMBEDDED_DB, VECS, ID)).returncode != 0:
    fail("import_vectors")
# Count only EMBEDDABLE clauses still NULL. Heading-only / "void" / table-stripped
# clauses have empty text and are skipped by the embedder (main.rs: text.trim()
# .is_empty()) — counting them keeps null_after > 0 forever and complete=1 never
# fires (mirrors store.CountNullAtFloor's embeddable-text predicate).
nul = duckdb_scalar(EMBEDDED_DB,
                    "SELECT count(*) FROM clauses WHERE embedding IS NULL AND length(trim(text)) > 0;")
say("null_after=%s" % nul)

# ---- version the partial DB + ledger back ----------------------------------
KU = os.environ.get("KAGGLE_USERNAME", "")
KK = os.environ.get("KAGGLE_KEY", "")
DS = os.environ.get("EMBED_STATE_DS") or (("%s/3gpp-rust-embedded-%s" % (KU, SHARD)) if KU else "")
if KU and KK and os.path.isfile(VECS):
    if have("kaggle") or sh("pip install --quiet kaggle").returncode == 0:
        out = "%s/state" % WORK
        os.makedirs(out, exist_ok=True)
        shutil.copy(EMBEDDED_DB, "%s/3gpp-embedded.duckdb" % out)
        shutil.copy(VECS, "%s/vecs.jsonl" % out)
        json.dump({"title": "3gpp-rust-embedded-%s" % SHARD, "id": DS, "licenses": [{"name": "CC0-1.0"}]},
                  open("%s/dataset-metadata.json" % out, "w"))
        if sh('kaggle datasets version -p "%s" -m "shard=%s null=%s" --dir-mode zip' % (out, SHARD, nul)).returncode == 0:
            say("versioned=%s" % DS)
        elif sh('kaggle datasets create -p "%s" --dir-mode zip' % out).returncode == 0:
            say("created=%s" % DS)
        else:
            say("version=skip detail=failed")
else:
    say("version=skip detail=no_creds_or_no_vecs")

# ---- shrink kernel output so the driver's pull is fast ---------------------
KEEP = {"3gpp-embedded.duckdb", "vecs.jsonl", "RESULT.txt"}
os.chdir(WORK)
for name in os.listdir(WORK):
    if name in KEEP:
        continue
    p = os.path.join(WORK, name)
    try:
        shutil.rmtree(p) if os.path.isdir(p) else os.remove(p)
    except Exception:
        pass

# Complete when no clause is left NULL for this shard.
if nul == "0":
    say("step=OK complete=1")
else:
    say("step=OK complete=0 (partial — resume next run)")
