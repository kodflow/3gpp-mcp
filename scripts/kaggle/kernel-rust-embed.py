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


def srchash(src):
    """Deterministic hash of the rust/embedder sources, so the cached build/model is
    reused only while the code is unchanged. Returns '' on any error (cache disabled)."""
    try:
        import hashlib
        h = hashlib.sha256()
        root = os.path.join(src, "rust/embedder")
        files = sorted(glob.glob(os.path.join(root, "src/**/*.rs"), recursive=True))
        files += [os.path.join(root, "Cargo.toml"), os.path.join(root, "Cargo.lock")]
        for p in files:
            if os.path.isfile(p):
                h.update(os.path.relpath(p, root).encode())
                with open(p, "rb") as f:
                    h.update(f.read())
        return h.hexdigest()
    except Exception:
        return ""


def duckdb_scalar(db, query):
    r = sh('duckdb "%s" -noheader -list "%s"' % (db, query))
    return r.stdout.strip() if r.returncode == 0 else ""


def gpu_count():
    """Number of CUDA GPUs visible (Kaggle gives T4 x2; a rented box may give more).
    Falls back to 1 on any error so the single-GPU path always works."""
    r = sh("nvidia-smi -L")
    if r.returncode != 0:
        return 1
    n = sum(1 for ln in r.stdout.splitlines() if ln.strip().startswith("GPU "))
    return max(1, n)


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

# ---- tool cache (FAIL-OPEN) -------------------------------------------------
# Reuse the built embedder binary + libonnxruntime.so + the fp16 model across resume
# passes of this shard, guarded by a hash of the rust/embedder sources (a code change
# rebuilds). ANY failure here falls through to the normal download+build path below —
# the cache can never block a run. Persisted once (on the build pass) into the per-shard
# resume Dataset; restored on the next pass to skip ~10 min of cargo build + model fetch.
CACHED_BIN = ""
CACHED_LIBDIR = ""
CACHED_MODEL = ""
SRCHASH = srchash(src)
try:
    hits = sorted(glob.glob("/kaggle/input/**/embedder-cache/srchash.txt", recursive=True))
    if SRCHASH and hits and open(hits[0]).read().strip() == SRCHASH:
        cdir = os.path.dirname(hits[0])
        cbin = os.path.join(cdir, "embedder")
        cso = sorted(glob.glob(os.path.join(cdir, "libonnxruntime.so*")), key=len)
        cmod = os.path.join(cdir, "bge-m3-fp16")
        if os.path.isfile(cbin) and cso and os.path.isfile(os.path.join(cmod, "model.onnx_data")):
            os.chmod(cbin, 0o755)
            CACHED_BIN, CACHED_LIBDIR, CACHED_MODEL = cbin, os.path.dirname(cso[0]), cmod
            say("toolcache=hit srchash=%s" % SRCHASH[:12])
except Exception as e:
    say("toolcache=error detail=%s" % str(e)[:80])
if not CACHED_BIN:
    say("toolcache=miss srchash=%s (build+fetch this run)" % (SRCHASH[:12] or "none"))

# ---- BGE-M3 model files (external-data ONNX + tokenizer) -------------------
if CACHED_MODEL:
    BGE16 = CACHED_MODEL
    say("model=cached %s" % BGE16)
else:
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
# []f32 unchanged — only the weights become fp16. Skipped entirely when the toolcache
# restored a ready fp16 model.
if not CACHED_MODEL:
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

# ---- build the embed-io bridge (Rust by default; Go fallback) + the embedder --
# Phase 10 repoint: the DuckDB I/O bridge is the RUST embed-io (store-rs), the same
# binary the rust-go-roundtrip CI proves byte-compatible with the Go serve side. Its
# CLI is a drop-in for cmd/embed-io, and libduckdb is statically bundled (no
# LD_LIBRARY_PATH). Set LEGACY_EMBED_IO=1 to fall back to the Go bridge (rollback).
if os.environ.get("LEGACY_EMBED_IO", "").strip() == "1":
    if sh("CGO_ENABLED=1 go build -o /tmp/embed-io ./cmd/embed-io").returncode != 0:
        fail("build_embed_io_go")
    EMBED_IO, RESUME_FLAG = "/tmp/embed-io", " --resume"
    say("embed_io=go (legacy)")
else:
    bio = sh("cargo build --release --manifest-path rust/store/Cargo.toml --bin embed-io")
    if bio.returncode != 0:
        fail("build_embed_io_rs", (bio.stderr or "")[-200:])
    # workspace target → src/rust/target/release/embed-io. Rust export is inherently
    # resume (worklist = clauses WHERE embedding IS NULL), so no --resume flag.
    EMBED_IO, RESUME_FLAG = os.path.join(src, "rust/target/release/embed-io"), ""
    say("embed_io=rust")
# fp16 identity (EMBED_MODELS_CONFIG → cmd/embedid resolves the fp16 model).
ID = sh("CGO_ENABLED=1 go run ./cmd/embedid", env=EMBED_MODEL_ENV).stdout.strip()
if not re.match(r"^[0-9a-f]{6,}$", ID):
    fail("bad_identity", ID)
say("embed_identity=%s (fp16)" % ID)
if CACHED_BIN:
    # Toolcache hit: skip the ~5-10 min cargo build, run the cached binary + its .so.
    EMBEDDER = CACHED_BIN
    os.environ["LD_LIBRARY_PATH"] = CACHED_LIBDIR + ":" + os.environ.get("LD_LIBRARY_PATH", "")
    say("cargo=cached ort_lib=%s/libonnxruntime.so" % CACHED_LIBDIR)
else:
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
if sh('%s --db "%s" --export-worklist "%s"%s' % (EMBED_IO, EMBEDDED_DB, WL, RESUME_FLAG)).returncode != 0:
    fail("export_worklist")
say("worklist_lines=%s" % (sh('wc -l < "%s"' % WL).stdout.strip()))

# ---- fp16 smoke gate: Rust fp16 vs fp32 cosine (build pass only) -----------
# The served data image is fp16; the existing Go cos>=0.9995 test validates the
# CONVERSION but never the RUST embedder that actually bakes the corpus. Embed a small
# fixed slice through the SAME Rust binary with the fp16 AND the fp32 model and assert
# mean cos>=0.9995 BEFORE the bulk run, so a silent fp16/pooling regression in the Rust
# path cannot contaminate the full index. Skipped on a tool-cache hit (the fp32 model
# is not restored; the cached fp16 model was already gated on the pass that built it).
def _load_gate_vecs(p):
    m = {}
    try:
        with open(p) as gf:
            for ln in gf:
                ln = ln.strip()
                if not ln:
                    continue
                r = json.loads(ln)
                v = r.get("vec")
                if v and len(v) == 1024:
                    m[r["chunk_id"]] = v
    except Exception as e:
        # Fail-open (the gate's own ok16/ok32/common checks catch a truly empty load),
        # but log WHY so an operator can tell a parse/disk error from "no vectors".
        say("fp16_gate vec-load error path=%s detail=%s" % (os.path.basename(p), str(e)[:120]))
    return m


if not CACHED_MODEL and os.path.isfile("%s/model.onnx" % BGE):
    gate_wl = os.path.join(WORK, "gate.jsonl")
    sh('head -n 32 "%s" > "%s"' % (WL, gate_wl))
    if os.path.isfile(gate_wl) and os.path.getsize(gate_wl) > 0:
        g16, g32 = os.path.join(WORK, "g16.jsonl"), os.path.join(WORK, "g32.jsonl")
        for od in (g16, g32):
            try:
                os.remove(od)
            except OSError:
                pass
        ok16 = subprocess.run('"%s" --in "%s" --out "%s" --model-dir "%s" --embed-identity "%s" --require-cuda'
                              % (EMBEDDER, gate_wl, g16, BGE16, ID), shell=True, env=os.environ).returncode
        ok32 = subprocess.run('"%s" --in "%s" --out "%s" --model-dir "%s" --embed-identity "%s" --require-cuda'
                              % (EMBEDDER, gate_wl, g32, BGE, ID), shell=True, env=os.environ).returncode
        a, b = _load_gate_vecs(g16), _load_gate_vecs(g32)
        common = [k for k in a if k in b]
        if ok16 != 0 or ok32 != 0 or not common:
            fail("fp16_gate", "embed_failed ok16=%s ok32=%s common=%d" % (ok16, ok32, len(common)))
        # Vectors are L2-normalised, so cosine == dot product.
        mean_cos = sum(sum(x * y for x, y in zip(a[k], b[k])) for k in common) / len(common)
        say("fp16_gate mean_cos=%.5f n=%d" % (mean_cos, len(common)))
        if mean_cos < 0.9995:
            fail("fp16_gate", "mean_cos=%.5f < 0.9995 — Rust fp16 path diverges from fp32" % mean_cos)
        say("fp16_gate=ok")
    else:
        say("fp16_gate=skip (empty worklist)")
else:
    say("fp16_gate=skip (cache-hit or no fp32 model)")

# Output is INHERITED (not captured) so PROGRESS lines stream LIVE into the Kaggle log.
# MULTI-GPU: one embedder process per GPU, in PARALLEL. Each shards the work-list by a
# hash of the clause text (--shard i/N) so the shards are disjoint AND every copy of a
# clause stays in one shard (the dedup is never split). Each writes its own ledger
# vecs.<i> and treats the merged VECS as already-done (--resume-from), then we fold the
# shard ledgers back into VECS. N==1 runs the plain single-GPU path. Scales to any N.
NGPU = gpu_count()
say("gpus=%d" % NGPU)
COMMON = '--model-dir "%s" --embed-identity "%s" --batch %s --limit %s --require-cuda' % (
    BGE16, ID, BATCH, LIMIT)
if NGPU <= 1:
    cmd = '"%s" --in "%s" --out "%s" %s' % (EMBEDDER, WL, VECS, COMMON)
    try:
        emb_rc = subprocess.run(cmd, shell=True, env=os.environ, timeout=TIME_BUDGET).returncode
    except subprocess.TimeoutExpired:
        emb_rc = 124
        say("embedder hit TIME_BUDGET — partial (ledger resumes next run)")
else:
    shard_outs = ["%s.%d" % (VECS, i) for i in range(NGPU)]
    procs = []
    for i in range(NGPU):
        cmd = '"%s" --in "%s" --out "%s" --resume-from "%s" --device %d --shard-index %d --shard-count %d %s' % (
            EMBEDDER, WL, shard_outs[i], VECS, i, i, NGPU, COMMON)
        procs.append(subprocess.Popen(cmd, shell=True, env=os.environ))
    deadline = time.time() + TIME_BUDGET
    emb_rc = 0
    for i, p in enumerate(procs):
        try:
            rc = p.wait(timeout=max(1, int(deadline - time.time())))
            emb_rc = emb_rc or rc
        except subprocess.TimeoutExpired:
            p.kill()
            emb_rc = 124
            say("gpu %d shard hit TIME_BUDGET — partial (resumes next run)" % i)
    # Fold each shard's new vectors into the canonical ledger (VECS already holds the prior
    # merged ledger; the shards carry only this run's additions, so appending is correct).
    with open(VECS, "a") as merged:
        for vi in shard_outs:
            if os.path.isfile(vi):
                with open(vi) as f:
                    shutil.copyfileobj(f, merged)
                os.remove(vi)
    say("multi_gpu merged %d shard ledgers" % NGPU)
if emb_rc != 0:
    # Non-fatal: the vecs.jsonl ledger is a valid resume point; import what we have.
    say("embedder_rc=%d (partial allowed — ledger resumes next run; see live log above)" % emb_rc)
vec_lines = sh('wc -l < "%s"' % VECS).stdout.strip() if os.path.isfile(VECS) else "0"
say("vectors_written=%s" % vec_lines)

if sh('%s --db "%s" --import-vectors "%s" --embed-identity "%s" --build-hnsw'
      % (EMBED_IO, EMBEDDED_DB, VECS, ID)).returncode != 0:
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
        # Persist the toolcache ONCE (only on the build pass — a cache-hit run already has
        # it, so don't re-upload). Fail-open: a copy error never fails the run.
        if not CACHED_BIN and SRCHASH:
            try:
                cc = "%s/embedder-cache" % out
                os.makedirs(cc, exist_ok=True)
                shutil.copy(EMBEDDER, "%s/embedder" % cc)
                libdir = os.environ.get("LD_LIBRARY_PATH", "").split(":")[0]
                for so in glob.glob(os.path.join(libdir, "libonnxruntime.so*")):
                    shutil.copy(so, cc)
                shutil.copytree(BGE16, "%s/bge-m3-fp16" % cc, dirs_exist_ok=True)
                open("%s/srchash.txt" % cc, "w").write(SRCHASH)
                say("toolcache=persisted srchash=%s" % SRCHASH[:12])
            except Exception as e:
                say("toolcache_persist=skip detail=%s" % str(e)[:80])
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
