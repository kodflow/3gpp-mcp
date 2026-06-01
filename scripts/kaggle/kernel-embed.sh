#!/usr/bin/env bash
# Kaggle T4 GPU kernel for the embed POC (PR-11a), ZERO-UPLOAD + self-diagnosing.
# Everything is pulled from PUBLIC sources on the kernel (Internet on). Writes
# compact single-line RESULT markers to /kaggle/working/RESULT.txt (the dev box
# reads those; the full stdout log has ANSI noise).
set -uo pipefail
R=/kaggle/working/RESULT.txt; : > "$R"
say(){ echo "RESULT $*" | tee -a "$R"; }
fail(){ say "FAIL=$1 detail=${2:-}"; exit 1; }

REPO="https://github.com/kodflow/3gpp-mcp"
BRANCH="${BRANCH:-feat/append-resume-hardening}"
FLOOR="${EMBED_FLOOR:-Rel-15}"
SERIES="${SERIES:-21}"
BGE_COMMIT="5617a9f61b028005a4858fdac845db406aefb181"
ORT_VERSION="${ORT_VERSION:-1.26.0}"
WORK=/kaggle/working; cd "$WORK"
unset EMBEDDER || true   # force the real onnx/CUDA backend (never the Local hash embedder)

say "step=start floor=$FLOOR series=$SERIES ort=$ORT_VERSION branch=$BRANCH"
# GPU-optional: use CUDA when a GPU is attached, else fall back to CPU so the
# pipeline (and the Internet/download path) is still exercised end-to-end.
if nvidia-smi -L >/tmp/gpu.txt 2>&1; then
  EP=cuda; ORTPKG="onnxruntime-linux-x64-gpu-${ORT_VERSION}"
  say "gpu=present detail=$(head -1 /tmp/gpu.txt | tr -d '\n') ep=cuda"
else
  EP=cpu; ORTPKG="onnxruntime-linux-x64-${ORT_VERSION}"
  say "gpu=absent ep=cpu (CPU fallback — GPU not attached to this worker)"
fi

apt-get update -qq >/dev/null 2>&1; apt-get install -y -qq zstd unzip >/dev/null 2>&1 || true
command -v zstd >/dev/null || fail no_zstd
if ! command -v go >/dev/null 2>&1; then
  curl -fsSL https://go.dev/dl/go1.23.4.linux-amd64.tar.gz -o /tmp/go.tgz || fail go_dl
  tar -C /usr/local -xzf /tmp/go.tgz; export PATH=/usr/local/go/bin:$PATH
fi
say "go=$(go version | awk '{print $3}')"
curl -fsSL -o /tmp/duckdb.zip https://github.com/duckdb/duckdb/releases/download/v1.1.3/duckdb_cli-linux-amd64.zip || fail duckdb_dl
mkdir -p /tmp/bin && (cd /tmp/bin && unzip -o -q /tmp/duckdb.zip); export PATH=/tmp/bin:$PATH
duckdb --version >/dev/null 2>&1 || fail duckdb_run

git clone --depth 1 -b "$BRANCH" "$REPO" "$WORK/src" >/tmp/clone.txt 2>&1 || fail clone "$(tail -1 /tmp/clone.txt)"
cd "$WORK/src"

curl -fsSL -o "$WORK/full.zst" "$REPO/releases/download/latest/3gpp.duckdb.zst" || fail db_dl
say "db_zst_bytes=$(stat -c %s "$WORK/full.zst")"
zstd -d --long=27 -f "$WORK/full.zst" -o "$WORK/full.duckdb" >/tmp/z.txt 2>&1 || fail decompress "$(tail -1 /tmp/z.txt)"
say "db_bytes=$(stat -c %s "$WORK/full.duckdb")"
FULLN=$(duckdb "$WORK/full.duckdb" -noheader -list "SELECT count(*) FROM clauses;" 2>/tmp/q.txt) || fail db_open "$(tail -1 /tmp/q.txt)"
say "full_clauses=$FULLN"
duckdb "$WORK/lexical.duckdb" "ATTACH '$WORK/full.duckdb' AS s (READ_ONLY); CREATE TABLE clauses AS SELECT * FROM s.clauses WHERE substr(spec_id,1,2)='$SERIES' AND release IN ('Rel-15','Rel-16','Rel-17','Rel-18','Rel-19','Rel-20');" 2>/tmp/sl.txt || fail slice "$(tail -1 /tmp/sl.txt)"
SLN=$(duckdb "$WORK/lexical.duckdb" -noheader -list "SELECT count(*) FROM clauses;")
say "sliced_clauses=$SLN"
[ "${SLN:-0}" -ge 1 ] || fail empty_slice "series=$SERIES floor=$FLOOR full=$FULLN"

curl -fsSL -o /tmp/ort.tgz "https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-linux-x64-gpu-${ORT_VERSION}.tgz" || fail ort_dl
mkdir -p "$WORK/ort"; tar -C "$WORK/ort" --strip-components=1 -xzf /tmp/ort.tgz || fail ort_untar
ORT_LIB="$(find "$WORK/ort" -name 'libonnxruntime.so*' | head -1)"; [ -n "$ORT_LIB" ] || fail ort_nolib
ORT_DIR="$(dirname "$ORT_LIB")"; say "ort_lib=$(basename "$ORT_LIB")"

BGE="$WORK/bge-m3"; mkdir -p "$BGE"; HF="https://huggingface.co/BAAI/bge-m3/resolve/${BGE_COMMIT}"
curl -fsSL "$HF/onnx/model.onnx"             -o "$BGE/model.onnx"             || fail hf_model
curl -fsSL "$HF/onnx/model.onnx_data"        -o "$BGE/model.onnx_data"        || fail hf_data
curl -fsSL "$HF/onnx/Constant_7_attr__value" -o "$BGE/Constant_7_attr__value" || fail hf_const
curl -fsSL "$HF/tokenizer.json"              -o "$BGE/tokenizer.json"         || fail hf_tok
say "model_data_bytes=$(stat -c %s "$BGE/model.onnx_data")"

export CGO_ENABLED=1
export ONNXRUNTIME_SHARED_LIBRARY_PATH="$ORT_LIB"
export LD_LIBRARY_PATH="$ORT_DIR:${LD_LIBRARY_PATH:-}"
go build -tags onnx -o "$WORK/embed" ./cmd/embed >/tmp/build.txt 2>&1 || fail build "$(tail -2 /tmp/build.txt | tr '\n' ' ')"
say "build=ok"

cp "$WORK/lexical.duckdb" "$WORK/3gpp-embedded.duckdb"
export BGE_M3_DIR="$BGE" ORT_EP=cuda
REPORT="$WORK/embed-report.json"; START=$(date +%s)
"$WORK/embed" --db "$WORK/3gpp-embedded.duckdb" --embed-floor "$FLOOR" --require-semantic --report json >"$REPORT" 2>/tmp/embed.err
RC=$?; ELAPSED=$(( $(date +%s) - START ))
if [ "$RC" != "0" ]; then say "embed_rc=$RC err=$(tail -2 /tmp/embed.err | tr '\n' ' ' | cut -c1-200)"; fail embed_run; fi
MODEL=$(python3 -c "import json;print(json.load(open('$REPORT')).get('model'))" 2>/dev/null)
CAND=$(python3 -c "import json;print(json.load(open('$REPORT')).get('candidates'))" 2>/dev/null)
EMB=$(python3 -c "import json;print(json.load(open('$REPORT')).get('embedded_clauses'))" 2>/dev/null)
NUL=$(python3 -c "import json;print(json.load(open('$REPORT')).get('null_embeddings_at_floor'))" 2>/dev/null)
say "model=$MODEL candidates=$CAND embedded=$EMB null_at_floor=$NUL elapsed=${ELAPSED}s ep=cuda"
[ "${NUL:-1}" = "0" ] && [ "${EMB:-0}" -ge 1 ] || fail incomplete "null=$NUL emb=$EMB"
awk -v e="$EMB" -v t="$ELAPSED" 'BEGIN{if(t>0)printf "RESULT throughput=%.2f clauses_per_s_T4\n",e/t}' | tee -a "$R"
say "step=OK"
