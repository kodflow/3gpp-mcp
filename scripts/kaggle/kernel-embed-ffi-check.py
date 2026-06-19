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
import urllib.request

BRANCH = os.environ.get("BRANCH", "main")
REPO = os.environ.get("REPO", "https://github.com/kodflow/3gpp-mcp")
QUERIES = os.environ.get("FFI_QUERIES", "AMF registration procedure|lawful interception X2 interface|UPF session establishment").split("|")
HF = "https://huggingface.co/BAAI/bge-m3/resolve/main"
WORK = "/kaggle/working"


def sh(cmd, env=None, check=False):
    print("+ " + cmd, flush=True)
    r = subprocess.run(cmd, shell=True, env=env or os.environ.copy(), text=True)
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
# avoids any CUDA-toolkit build/link risk on the Kaggle image. We CAPTURE the build output
# so a failure is diagnosable from ffi-check.json (the kaggle log is not always pulled).
b = subprocess.run(
    f"cd {src} && cargo build --release --manifest-path rust/embed-core/Cargo.toml --features ort --bin embed-core-check",
    shell=True, env=os.environ.copy(), text=True, capture_output=True,
)
if b.returncode != 0:
    tail = (b.stderr or "")[-3000:]
    with open(os.path.join(WORK, "ffi-check.json"), "w") as f:
        json.dump({"ok": False, "stage": "cargo build --features ort", "rc": b.returncode, "stderr_tail": tail}, f, indent=2)
    print(tail, flush=True)
    fail("cargo build embed-core --features ort")

# --- run the check with the real model (CPU) ---------------------------------
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
