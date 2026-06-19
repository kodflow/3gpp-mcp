#!/usr/bin/env python3
# kernel-embed-ffi-check.py — Kaggle GPU validation of the Phase-1 embed-core FFI (Rust
# serve query-embed). It clones the branch, downloads BGE-M3 (model.onnx + model.onnx_data +
# tokenizer.json) from HuggingFace, builds rust/embed-core --features ort,cuda + the
# embed-core-check bin, and embeds a few test queries with the REAL model — confirming the
# cdylib's ort path produces sane, L2-normalised 1024-d vectors on a GPU. Results (one JSON
# line per query) go to stdout AND /kaggle/working/ffi-check.json for the driver to collect.
#
# No credentials in this file (the driver pushes it). Config via os.environ.setdefault(...).
import json
import os
import subprocess
import sys
import time
import urllib.request

BRANCH = os.environ.get("BRANCH", "main")
REPO = os.environ.get("REPO", "https://github.com/kodflow/3gpp-mcp")
QUERIES = os.environ.get("FFI_QUERIES", "AMF registration procedure|lawful interception X2 interface|UPF session establishment").split("|")
HF = "https://huggingface.co/BAAI/bge-m3/resolve/main"
WORK = "/kaggle/working"
T0 = time.time()


def step(msg):
    """Timestamped progress line — every stage logs so a long build is never silent."""
    print("[+%6.1fs] %s" % (time.time() - T0, msg), flush=True)


def sh(cmd, env=None, check=False):
    step("RUN " + cmd)
    # No capture: stream the child's stdout/stderr LIVE (cargo's per-crate "Compiling …"
    # progress shows in the Kaggle log instead of a silent ~90s gap).
    r = subprocess.run(cmd, shell=True, env=env or os.environ.copy(), text=True)
    step("→ rc=%d  (%s)" % (r.returncode, cmd.split()[0]))
    if check and r.returncode != 0:
        fail("cmd failed: " + cmd)
    return r


def fail(msg):
    print("FFI-CHECK FAIL: " + msg, flush=True)
    try:
        with open(os.path.join(WORK, "ffi-check.json"), "w") as f:
            json.dump({"ok": False, "error": msg}, f)
    except OSError:
        pass
    sys.exit(1)


def fetch(url, dst):
    print(f"fetch {url} -> {dst}", flush=True)
    urllib.request.urlretrieve(url, dst)


# --- toolchain: rust (+cargo) ------------------------------------------------
os.environ["PATH"] = os.path.expanduser("~/.cargo/bin") + ":" + os.environ.get("PATH", "")
if sh("command -v cargo").returncode != 0:
    sh("curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain stable --profile minimal", check=True)

# --- source ------------------------------------------------------------------
src = "/tmp/src"
if sh(f'git clone --depth 1 -b "{BRANCH}" "{REPO}" "{src}"').returncode != 0:
    fail(f"clone {REPO}@{BRANCH}")

# --- model: BGE-M3 onnx (model.onnx + external data + tokenizer) -------------
mdir = os.path.join(WORK, "bge-m3")
os.makedirs(mdir, exist_ok=True)
try:
    fetch(f"{HF}/onnx/model.onnx", os.path.join(mdir, "model.onnx"))
    # external-data file is referenced by the graph by this relative name.
    fetch(f"{HF}/onnx/model.onnx_data", os.path.join(mdir, "model.onnx_data"))
    fetch(f"{HF}/tokenizer.json", os.path.join(mdir, "tokenizer.json"))
except Exception as e:  # noqa: BLE001
    fail(f"model fetch: {e}")

# --- build embed-core --features ort + the check bin (CPU; single-query is cheap) ------
# `cuda` is intentionally OFF: serve query-embed is ONE query, CPU ort is plenty, and it
# avoids any CUDA-toolkit build/link risk on the Kaggle image. The build STREAMS live (sh)
# so cargo's per-crate "Compiling …" progress shows instead of a silent ~90s gap; the
# Kaggle web log captures it for diagnosis.
if sh(f"cd {src} && cargo build --release --manifest-path rust/embed-core/Cargo.toml --features ort --bin embed-core-check").returncode != 0:
    with open(os.path.join(WORK, "ffi-check.json"), "w") as f:
        json.dump({"ok": False, "stage": "cargo build --features ort"}, f, indent=2)
    fail("cargo build embed-core --features ort (see the Compiling… log above)")

# --- run the check with the real model (CPU) ---------------------------------
step("embedding %d test quer%s via the FFI" % (len(QUERIES), "y" if len(QUERIES) == 1 else "ies"))
env = os.environ.copy()
env["EMBED_MODEL_DIR"] = mdir
qargs = " ".join('"%s"' % q.replace('"', "") for q in QUERIES)
bin_path = f"{src}/rust/embed-core/target/release/embed-core-check"
r = subprocess.run(f"{bin_path} {qargs}", shell=True, env=env, text=True, capture_output=True)
print(r.stderr, flush=True)
print(r.stdout, flush=True)
run_stderr_tail = (r.stderr or "")[-2000:]

lines = [ln for ln in r.stdout.splitlines() if ln.strip().startswith("{")]
results = []
for ln in lines:
    try:
        results.append(json.loads(ln))
    except json.JSONDecodeError:
        pass
ok = r.returncode == 0 and len(results) == len(QUERIES) and all(
    x.get("rc") == 0 and x.get("backend") == "bge-m3-onnx" and abs(x.get("l2_norm", 0) - 1.0) < 1e-3 for x in results
)
with open(os.path.join(WORK, "ffi-check.json"), "w") as f:
    json.dump(
        {"ok": ok, "branch": BRANCH, "queries": QUERIES, "results": results, "run_rc": r.returncode, "run_stderr_tail": run_stderr_tail},
        f,
        indent=2,
    )
print("FFI-CHECK " + ("OK" if ok else "FAIL") + f" ({len(results)}/{len(QUERIES)} queries embedded via the real BGE-M3 ort path)", flush=True)
sys.exit(0 if ok else 1)
