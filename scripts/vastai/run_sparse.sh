#!/usr/bin/env bash
# =============================================================================
#  scripts/vastai/run_sparse.sh — SPARSE (learned-lexical) embed of the corpus gap on a
#  rented GPU box. Portable core of scripts/kaggle/kernel-sparse-corpus.py minus Kaggle glue.
# =============================================================================
#  Runs INSIDE the rented box (onstart). Pulls the lexical base + resumes from GHCR
#  3gpp-sparse (overlay), embeds the still-missing clause_sparse with embed-core-sparse
#  (fp16 dual-head, CUDA EP), and exports /workspace/repo/shard/sparse-shard.duckdb.zst.
#  It does NOT push — the runner pulls it back and publishes to 3gpp-sparse with the write PAT.
#
#  Env: GHCR_PAT (read), GHCR_OWNER, REF; optional EMBED_LIMIT (0 = whole gap).
#  Single GPU (a 4090 is one device); embed-core-sparse honours CUDA_VISIBLE_DEVICES.
# =============================================================================
set -euo pipefail

: "${GHCR_PAT:?read-only GHCR token required}"
GHCR_OWNER="${GHCR_OWNER:-kodflow}"
REF="${REF:-main}"
EMBED_LIMIT="${EMBED_LIMIT:-0}"
WORK="${WORK:-/workspace/work}"; SRC="${SRC:-/workspace/repo}"
mkdir -p "$WORK"
DB="$WORK/3gpp.duckdb"; MDIR="$WORK/bge-m3-sparse"
SHARD_OUT="$SRC/shard"; mkdir -p "$SHARD_OUT"
say() { echo "RESULT $*"; }

# ---- 1. toolchain (crane, oras, duckdb, zstd, go, cargo, python) -----------------
export DEBIAN_FRONTEND=noninteractive
command -v curl >/dev/null || { apt-get update -y && apt-get install -y --no-install-recommends curl ca-certificates git python3 python3-pip; }
command -v zstd >/dev/null || apt-get install -y --no-install-recommends zstd
b=/usr/local/bin
command -v crane >/dev/null || curl -fsSL "https://github.com/google/go-containerregistry/releases/download/v0.20.2/go-containerregistry_Linux_x86_64.tar.gz" | tar -xz -C "$b" crane
command -v oras  >/dev/null || curl -fsSL "https://github.com/oras-project/oras/releases/download/v1.2.0/oras_1.2.0_linux_amd64.tar.gz" | tar -xz -C "$b" oras
command -v duckdb >/dev/null || { curl -fsSL -o /tmp/d.zip "https://github.com/duckdb/duckdb/releases/download/v1.1.3/duckdb_cli-linux-amd64.zip" && (cd "$b" && unzip -o -q /tmp/d.zip); }
command -v cargo >/dev/null || { curl -fsSL https://sh.rustup.rs | sh -s -- -y --default-toolchain stable --profile minimal; export PATH="$HOME/.cargo/bin:$PATH"; }

# ---- 2. export the dual-head (dense+sparse) fp16 ONNX (mirrors kernel-sparse-corpus.py) --
python3 -m pip install -q FlagEmbedding onnx onnxruntime onnxscript onnxconverter-common torch 2>/dev/null || true
mkdir -p "$MDIR"
python3 - "$MDIR" <<'PY'
import sys, os, torch, onnx
import torch.nn as nn, torch.nn.functional as F
from torch.export import Dim
from FlagEmbedding import BGEM3FlagModel
from onnxconverter_common import float16
MDIR = sys.argv[1]
m3 = BGEM3FlagModel("BAAI/bge-m3", use_fp16=False)
backbone = m3.model.model; sparse_linear = m3.model.sparse_linear
class DenseSparse(nn.Module):
    def __init__(self, b, s): super().__init__(); self.backbone, self.sparse_linear = b, s
    def forward(self, input_ids, attention_mask):
        h = self.backbone(input_ids=input_ids, attention_mask=attention_mask, return_dict=True).last_hidden_state
        dense = F.normalize(h[:, 0], p=2, dim=-1)
        sparse = F.relu(self.sparse_linear(h)).squeeze(-1) * attention_mask.to(h.dtype)
        return dense, sparse
enc = m3.tokenizer(["AMF SMF N4 registration procedure", "short"], return_tensors="pt", padding=True, truncation=True, max_length=8192)
_b, _s = Dim("batch", min=1, max=512), Dim("seq", min=1, max=8192)
onnx_path = MDIR + "/model.onnx"
torch.onnx.export(DenseSparse(backbone, sparse_linear).eval(), (enc["input_ids"], enc["attention_mask"]), onnx_path,
    dynamo=True, external_data=True, input_names=["input_ids","attention_mask"], output_names=["sentence_embedding","sparse_weights"],
    dynamic_shapes={"input_ids":{0:_b,1:_s},"attention_mask":{0:_b,1:_s}}, opset_version=18)
if not os.path.exists(MDIR+"/model.onnx_data"):
    onnx.save_model(onnx.load(onnx_path), onnx_path, save_as_external_data=True, all_tensors_to_one_file=True, location="model.onnx_data", size_threshold=1024)
try:
    m16 = float16.convert_float_to_float16(onnx.load(onnx_path), keep_io_types=True, disable_shape_infer=True)
    os.remove(onnx_path); os.path.exists(MDIR+"/model.onnx_data") and os.remove(MDIR+"/model.onnx_data")
    onnx.save_model(m16, onnx_path, save_as_external_data=True, all_tensors_to_one_file=True, location="model.onnx_data", size_threshold=1024)
    print("fp16 ok")
except Exception as e:
    print("fp16 skipped", e)
m3.tokenizer.save_pretrained(MDIR)
PY
[ -f "$MDIR/model.onnx_data" ] || { say "fail model_export_no_external_data"; exit 1; }

# ---- 3. pull base + resume overlay from 3gpp-sparse ------------------------------
printf %s "$GHCR_PAT" | crane auth login ghcr.io -u "$GHCR_OWNER" --password-stdin
crane export "ghcr.io/${GHCR_OWNER}/3gpp-corpus:latest" - | tar -xC "$WORK" 3gpp.duckdb
cd "$SRC"
cargo build --release --manifest-path rust/store/Cargo.toml --bin embed-io --bin overlay
cargo build --release --manifest-path rust/embed-core/Cargo.toml --bin embed-core-sparse --features ort,cuda
EMBED_IO="$SRC/rust/target/release/embed-io"; OVERLAY="$SRC/rust/target/release/overlay"
SPARSE="$SRC/rust/embed-core/target/release/embed-core-sparse"
ORT_SO="$(find "$SRC/rust/embed-core/target/release" -name 'libonnxruntime.so*' | head -1)"
export EMBED_MODEL_DIR="$MDIR" ORT_DYLIB_PATH="$ORT_SO" ORT_EP=cuda
if printf %s "$GHCR_PAT" | oras login ghcr.io -u "$GHCR_OWNER" --password-stdin 2>/dev/null \
  && oras pull "ghcr.io/${GHCR_OWNER}/3gpp-sparse:latest" -o "$WORK/resume" 2>/dev/null \
  && [ -f "$WORK/resume/sparse-shard.duckdb.zst" ]; then
  zstd -d -f "$WORK/resume/sparse-shard.duckdb.zst" -o "$WORK/resume/sparse-shard.duckdb"
  "$OVERLAY" --base "$DB" --vec "$WORK/resume/sparse-shard.duckdb"
  say "resume_carried=$(duckdb "$DB" -noheader -list 'SELECT count(*) FROM clause_sparse;' 2>/dev/null || echo 0)"
else
  say "no prior 3gpp-sparse shard — fresh sparse pass"
fi

# ---- 4. embed the gap → import → export shard (NO push) --------------------------
SID="$(EMBED_MODEL=bge-m3-sparse EMBED_MODELS_CONFIG=/dev/null CGO_ENABLED=0 go run ./cmd/embedid --sparse 2>/dev/null || echo bge-m3-sparse)"
lim=""; [ "$EMBED_LIMIT" != 0 ] && lim=" --limit $EMBED_LIMIT"
"$EMBED_IO" --db "$DB" --export-sparse-worklist "$WORK/sparse-wl.jsonl"$lim
"$SPARSE" --in "$WORK/sparse-wl.jsonl" --out "$WORK/sparse-post.jsonl"
"$EMBED_IO" --db "$DB" --import-sparse "$WORK/sparse-post.jsonl" --sparse-model "$SID"
# Ship a cumulative shard (clauses touched by clause_sparse) for the runner to publish.
duckdb "$SHARD_OUT/sparse-shard.duckdb" "ATTACH '$DB' AS f (READ_ONLY);
  CREATE TABLE clauses AS SELECT chunk_id, spec_id, release, version, clause_path, text,
    CAST(NULL AS FLOAT[1024]) AS embedding, CAST(NULL AS VARCHAR) AS embedding_hash
    FROM f.clauses WHERE chunk_id IN (SELECT chunk_id FROM f.clause_sparse);
  CREATE TABLE clause_sparse AS SELECT chunk_id, term_id, weight FROM f.clause_sparse;"
zstd -19 -T0 -f "$SHARD_OUT/sparse-shard.duckdb" -o "$SHARD_OUT/sparse-shard.duckdb.zst"
rm -f "$SHARD_OUT/sparse-shard.duckdb"
ROWS="$(duckdb "$DB" -noheader -list 'SELECT count(DISTINCT chunk_id) FROM clause_sparse;')"
say "step=OK sparse_model=$SID clause_sparse_rows=$ROWS"
