#!/usr/bin/env bash
# =============================================================================
#  scripts/vastai/run_embed.sh — DENSE embed of one 3GPP series shard on a rented
#  GPU box (vast.ai RTX 4090). The portable core of scripts/kaggle/kernel-rust-embed.py
#  with the Kaggle glue (Dataset mount/persist, 12h watchdog, /kaggle/working) removed.
# =============================================================================
#  Runs INSIDE the rented box (onstart). It NEVER pushes to GHCR — it produces
#  /workspace/repo/shard/s<SERIES>.duckdb.zst, which the trusted CI runner pulls back
#  (vastai copy) and publishes with the write PAT (scripts/ci/publish-vec.sh). The box
#  only holds a READ-ONLY GHCR_PAT. Resume-from-GHCR is layered on in a later commit.
#
#  Env (from the instance --env): GHCR_PAT (read), GHCR_OWNER, SERIES, REF.
#  Single GPU only (a 4090 is one device) — the 2xT4 --shard fan-out is Kaggle-specific.
# =============================================================================
set -euo pipefail

: "${GHCR_PAT:?read-only GHCR token required}" "${SERIES:?2-digit series required}"
GHCR_OWNER="${GHCR_OWNER:-kodflow}"
REF="${REF:-main}"
EMBED_BATCH="${EMBED_BATCH:-64}"
WORK="${WORK:-/workspace/work}"
SRC="${SRC:-/workspace/repo}"
mkdir -p "$WORK"
EMBEDDED_DB="$WORK/3gpp-embedded.duckdb"
VECS="$WORK/vecs.jsonl"
SHARD_OUT="$SRC/shard"
mkdir -p "$SHARD_OUT"

say() { echo "RESULT $*"; }

# ---- 1. Toolchain (bare CUDA image): crane, oras, duckdb, zstd, go, cargo --------
need() { command -v "$1" >/dev/null 2>&1; }
export DEBIAN_FRONTEND=noninteractive
need curl || { apt-get update -y && apt-get install -y --no-install-recommends curl ca-certificates git; }
need zstd || apt-get install -y --no-install-recommends zstd
need unzip || apt-get install -y --no-install-recommends unzip # duckdb CLI ships as a .zip
bins=/usr/local/bin
need crane || curl -fsSL "https://github.com/google/go-containerregistry/releases/download/v0.20.2/go-containerregistry_Linux_x86_64.tar.gz" | tar -xz -C "$bins" crane
need oras  || curl -fsSL "https://github.com/oras-project/oras/releases/download/v1.2.0/oras_1.2.0_linux_amd64.tar.gz" | tar -xz -C "$bins" oras
need duckdb || { curl -fsSL -o /tmp/d.zip "https://github.com/duckdb/duckdb/releases/download/v1.1.3/duckdb_cli-linux-amd64.zip" && (cd "$bins" && unzip -o -q /tmp/d.zip); }
if ! need go; then
  gov="$(curl -fsSL "https://raw.githubusercontent.com/${GHCR_OWNER}/3gpp-mcp/${REF}/go.mod" | sed -n 's/^go \([0-9.]*\).*/\1/p' | head -1)"
  curl -fsSL "https://go.dev/dl/go${gov}.linux-amd64.tar.gz" -o /tmp/go.tgz && rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz
  export PATH="/usr/local/go/bin:$PATH"
fi
if ! need cargo; then
  curl -fsSL https://sh.rustup.rs | sh -s -- -y --default-toolchain stable --profile minimal
  export PATH="$HOME/.cargo/bin:$PATH"
fi
say "toolchain ok go=$(go version | awk '{print $3}') cargo=$(cargo --version | awk '{print $2}')"

# ---- 2. Pull the private lexical base + slice this series ------------------------
printf %s "$GHCR_PAT" | crane auth login ghcr.io -u "$GHCR_OWNER" --password-stdin
crane export "ghcr.io/${GHCR_OWNER}/3gpp-corpus:latest" - | tar -xC "$WORK" 3gpp.duckdb
# Slice = only this series' clauses (mirrors kernel-rust-embed.py:216-226). embedding/_hash
# start NULL → the worklist below is exactly the un-embedded clauses for the series.
duckdb "$EMBEDDED_DB" "ATTACH '$WORK/3gpp.duckdb' AS f (READ_ONLY);
  CREATE TABLE clauses AS SELECT chunk_id, spec_id, release, version, clause_path, heading, text,
    is_normative, CAST(NULL AS FLOAT[1024]) AS embedding, CAST(NULL AS VARCHAR) AS embedding_hash
  FROM f.clauses WHERE substr(spec_id,1,2)='${SERIES}';"
say "sliced series=${SERIES} clauses=$(duckdb "$EMBEDDED_DB" -noheader -list 'SELECT count(*) FROM clauses;')"

# ---- 3. Build embed-io (store-rs) + the dense embedder (rust/embedder) -----------
# rust-bins ships embed-io/overlay/embed-core-sparse but NOT the dense embedder, so build
# it from source (fast on the 4090 box's CPU). embed-io tree-match prebuilt is a future opt.
cd "$SRC"
cargo build --release --manifest-path rust/store/Cargo.toml --bin embed-io --bin overlay
cargo build --release --manifest-path rust/embedder/Cargo.toml
EMBED_IO="$SRC/rust/target/release/embed-io"
EMBEDDER="$SRC/rust/embedder/target/release/embedder"
# ort load-dynamic: point at the libonnxruntime.so cargo fetched under target/.
ORT_SO="$(find "$SRC/rust/embedder/target/release" -name 'libonnxruntime.so*' | head -1)"
[ -n "$ORT_SO" ] && export LD_LIBRARY_PATH="$(dirname "$ORT_SO"):${LD_LIBRARY_PATH:-}"

# ---- 4. fp16 model + identity (MUST match the served data-image bake) ------------
# fp16 keep_io_types: weights fp16, IO fp32 (the embedder reads []f32). The served image is
# fp16, so the EmbedIdentity must be fp16 or semantic is refused at serve.
BGE="$WORK/bge-m3"; BGE16="$WORK/bge-m3-fp16"
python3 -m pip install -q huggingface_hub onnx onnxconverter-common 2>/dev/null || true
python3 - "$BGE" <<'PY'
import sys
from huggingface_hub import snapshot_download
# Pinned revision (matches models.yaml identity in kernel-rust-embed.py) — immutable fetch.
snapshot_download("BAAI/bge-m3", revision="5617a9f", local_dir=sys.argv[1], allow_patterns=["*.onnx","*.onnx_data","tokenizer*","*.json","sentencepiece*","*.model"])
PY
mkdir -p "$BGE16"
python3 - "$BGE" "$BGE16" <<'PY'
import sys, glob, shutil, os, onnx
from onnxconverter_common import float16
src, dst = sys.argv[1], sys.argv[2]
m = onnx.load(glob.glob(os.path.join(src, "*.onnx"))[0])
onnx.save(float16.convert_float_to_float16(m, keep_io_types=True), os.path.join(dst, "model.onnx"))
for f in glob.glob(os.path.join(src, "tokenizer*")) + glob.glob(os.path.join(src, "*.json")) + glob.glob(os.path.join(src, "*.model")):
    shutil.copy(f, dst)
print("fp16_saved")
PY
# models.yaml + cmd/embedid → the EmbedIdentity string (fields match corpus-data-image.yml).
cat > "$WORK/models.yaml" <<EOF
active: bge-m3-fp16
models:
  - name: bge-m3-fp16
    dim: 1024
    precision: fp16
    dir: $BGE16
EOF
ID="$(EMBED_MODEL=bge-m3-fp16 EMBED_MODELS_CONFIG="$WORK/models.yaml" CGO_ENABLED=0 go run ./cmd/embedid)"
say "embed_identity=$ID"

# ---- 4b. RESUME from GHCR 3gpp-vec (gap-only re-embed) ---------------------------
# Pull the prior dense shard for this series and overlay its vectors onto the freshly
# sliced base, so export-worklist below emits ONLY the still-unembedded clauses. This is
# the dense analogue of the sparse overlay-resume; it survives an interrupted box without
# re-embedding done work. NB: the GHCR union (post-job, runner side) is where the shard
# came from — on vast.ai the box resumes from GHCR directly, not a Kaggle Dataset.
OVERLAY="$SRC/rust/target/release/overlay"
PRIOR="$WORK/prior.duckdb"
if printf %s "$GHCR_PAT" | oras login ghcr.io -u "$GHCR_OWNER" --password-stdin 2>/dev/null \
  && oras pull "ghcr.io/${GHCR_OWNER}/3gpp-vec:latest-fp16" -o "$WORK/vec" 2>/dev/null \
  && [ -f "$WORK/vec/s${SERIES}.duckdb.zst" ]; then
  zstd -d -f "$WORK/vec/s${SERIES}.duckdb.zst" -o "$PRIOR"
  # Reject a non-DuckDB blob (DUCK magic at offset 8) before overlaying.
  if [ "$(dd if="$PRIOR" bs=1 skip=8 count=4 2>/dev/null)" = DUCK ]; then
    "$OVERLAY" --base "$EMBEDDED_DB" --vec "$PRIOR" --embedding-model bge-m3-fp16
    say "resume_overlaid=$(duckdb "$EMBEDDED_DB" -noheader -list 'SELECT count(*) FROM clauses WHERE embedding IS NOT NULL;')"
  else
    say "prior s${SERIES} shard invalid (no DUCK magic) — fresh embed"
  fi
else
  say "no prior s${SERIES} shard on 3gpp-vec — fresh embed"
fi

# ---- 5. export worklist → embed (single GPU) → import + HNSW ---------------------
WL="$WORK/work.jsonl"
"$EMBED_IO" --db "$EMBEDDED_DB" --export-worklist "$WL"
say "worklist_lines=$(wc -l < "$WL")"
"$EMBEDDER" --in "$WL" --out "$VECS" --model-dir "$BGE16" --embed-identity "$ID" --require-cuda --batch "$EMBED_BATCH"
"$EMBED_IO" --db "$EMBEDDED_DB" --import-vectors "$VECS" --embed-identity "$ID" --build-hnsw
NULL_AFTER="$(duckdb "$EMBEDDED_DB" -noheader -list "SELECT count(*) FROM clauses WHERE text<>'' AND embedding IS NULL;")"
say "imported vecs=$(wc -l < "$VECS") null_after=$NULL_AFTER"

# ---- 6. export the shard (NO push — the runner publishes) ------------------------
zstd -19 -T0 -f "$EMBEDDED_DB" -o "$SHARD_OUT/s${SERIES}.duckdb.zst"
ls -la "$SHARD_OUT"
say "step=OK shard=s${SERIES} null_after=$NULL_AFTER"
