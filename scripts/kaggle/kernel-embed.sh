#!/usr/bin/env bash
# Kaggle GPU kernel for the embed campaign (PR-11b), ZERO-UPLOAD + RESUMABLE +
# self-diagnosing. Everything is pulled from PUBLIC sources on the kernel
# (Internet on). Writes compact single-line RESULT markers to
# /kaggle/working/RESULT.txt (the driver reads those; full stdout has ANSI noise).
#
# Resume model (the load-bearing primitive for the 12h cap):
#   - Each series keeps ONE output Kaggle Dataset  ${KAGGLE_USERNAME}/3gpp-embedded-s<NN>
#     holding 3gpp-embedded.duckdb (the partially-embedded sub-base).
#   - START: if that dataset is mounted as input, resume FROM it (its
#     embedding_hash markers make embed skip already-done clauses); else build a
#     fresh lexical slice from the published `latest` DB.
#   - END: version the embedded DB back to the dataset (only when creds present and
#     something was embedded this run), so the NEXT session continues where this
#     one was killed. recent-first ordering means a kill leaves the NEWEST releases
#     done, not a random chunk_id prefix.
#
# Env knobs (all optional): SERIES, EMBED_FLOOR, BRANCH, ORT_VERSION, CHECKPOINT_EVERY,
#   RESUME_DB (explicit path to a mounted prior DB), EMBED_STATE_DS (dataset slug to
#   version into), EMBED_TIME_BUDGET (seconds before the run is told to stop).
set -uo pipefail
R=/kaggle/working/RESULT.txt; : > "$R"
say(){ echo "RESULT $*" | tee -a "$R"; }
fail(){ say "FAIL=$1 detail=${2:-}"; exit 1; }

REPO="https://github.com/kodflow/3gpp-mcp"
BRANCH="${BRANCH:-feat/embed-maximization}"
FLOOR="${EMBED_FLOOR:-Rel-15}"
SERIES="${SERIES:-21}"
BGE_COMMIT="5617a9f61b028005a4858fdac845db406aefb181"
ORT_VERSION="${ORT_VERSION:-1.26.0}"
CHECKPOINT_EVERY="${CHECKPOINT_EVERY:-2000}"
# Stop ~10.8h in so the version/validate tail always runs before Kaggle's 12h kill.
TIME_BUDGET="${EMBED_TIME_BUDGET:-39000}"
WORK=/kaggle/working; cd "$WORK" || { echo "RESULT FAIL=no_workdir"; exit 1; }
unset EMBEDDER || true   # force the real onnx/CUDA backend (never the Local hash embedder)

say "step=start floor=$FLOOR series=$SERIES ort=$ORT_VERSION branch=$BRANCH budget=${TIME_BUDGET}s"
# GPU-optional: CUDA when a GPU is attached (the fp16 Tensor-Core path on T4), else
# CPU so the pipeline + download path are still exercised end-to-end.
if nvidia-smi -L >/tmp/gpu.txt 2>&1; then
  EP=cuda
  say "gpu=present detail=$(head -1 /tmp/gpu.txt | tr -d '\n') ep=cuda"
else
  EP=cpu
  say "gpu=absent ep=cpu (CPU fallback — GPU not attached to this worker)"
fi

apt-get update -qq >/dev/null 2>&1; apt-get install -y -qq zstd unzip >/dev/null 2>&1 || true
command -v zstd >/dev/null || fail no_zstd
if ! command -v go >/dev/null 2>&1; then
  curl -fsSL --retry 5 https://go.dev/dl/go1.23.4.linux-amd64.tar.gz -o /tmp/go.tgz || fail go_dl
  tar -C /usr/local -xzf /tmp/go.tgz; export PATH=/usr/local/go/bin:$PATH
fi
say "go=$(go version | awk '{print $3}')"
curl -fsSL --retry 5 -o /tmp/duckdb.zip https://github.com/duckdb/duckdb/releases/download/v1.1.3/duckdb_cli-linux-amd64.zip || fail duckdb_dl
mkdir -p /tmp/bin && (cd /tmp/bin && unzip -o -q /tmp/duckdb.zip); export PATH=/tmp/bin:$PATH
duckdb --version >/dev/null 2>&1 || fail duckdb_run

git clone --depth 1 -b "$BRANCH" "$REPO" "$WORK/src" >/tmp/clone.txt 2>&1 \
  || { curl -fsSL --retry 5 -o /tmp/src.tgz "$REPO/archive/refs/heads/$BRANCH.tar.gz" \
        && mkdir -p "$WORK/src" && tar -C "$WORK/src" --strip-components=1 -xzf /tmp/src.tgz; } \
  || fail clone "$(tail -1 /tmp/clone.txt)"
cd "$WORK/src" || fail no_srcdir

# ---- RESUME or fresh SLICE --------------------------------------------------
EMBEDDED_DB="$WORK/3gpp-embedded.duckdb"
RESUME_DB="${RESUME_DB:-/kaggle/input/3gpp-embedded-s${SERIES}/3gpp-embedded.duckdb}"
if [ -f "$RESUME_DB" ]; then
  cp "$RESUME_DB" "$EMBEDDED_DB" || fail resume_copy
  PRIOR=$(duckdb "$EMBEDDED_DB" -noheader -list "SELECT count(*) FROM clauses WHERE embedding IS NOT NULL;" 2>/dev/null)
  say "resume=present src=$RESUME_DB prior_embedded=${PRIOR:-?}"
else
  say "resume=absent (fresh slice from published latest)"
  curl -fsSL --retry 5 -o "$WORK/full.zst" "$REPO/releases/download/latest/3gpp.duckdb.zst" || fail db_dl
  say "db_zst_bytes=$(stat -c %s "$WORK/full.zst")"
  zstd -d --long=27 -f "$WORK/full.zst" -o "$WORK/full.duckdb" >/tmp/z.txt 2>&1 || fail decompress "$(tail -1 /tmp/z.txt)"
  FULLN=$(duckdb "$WORK/full.duckdb" -noheader -list "SELECT count(*) FROM clauses;" 2>/tmp/q.txt) || fail db_open "$(tail -1 /tmp/q.txt)"
  say "full_clauses=$FULLN"
  duckdb "$EMBEDDED_DB" "ATTACH '$WORK/full.duckdb' AS s (READ_ONLY); CREATE TABLE clauses AS SELECT * FROM s.clauses WHERE substr(spec_id,1,2)='$SERIES';" 2>/tmp/sl.txt || fail slice "$(tail -1 /tmp/sl.txt)"
  SLN=$(duckdb "$EMBEDDED_DB" -noheader -list "SELECT count(*) FROM clauses;")
  say "sliced_clauses=$SLN"
  [ "${SLN:-0}" -ge 1 ] || fail empty_slice "series=$SERIES full=$FULLN"
fi

# ---- ONNX Runtime (GPU) + BGE-M3 -------------------------------------------
curl -fsSL --retry 5 -o /tmp/ort.tgz "https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-linux-x64-gpu-${ORT_VERSION}.tgz" || fail ort_dl
mkdir -p "$WORK/ort"; tar -C "$WORK/ort" --strip-components=1 -xzf /tmp/ort.tgz || fail ort_untar
ORT_LIB="$(find "$WORK/ort" -name 'libonnxruntime.so*' | head -1)"; [ -n "$ORT_LIB" ] || fail ort_nolib
ORT_DIR="$(dirname "$ORT_LIB")"; say "ort_lib=$(basename "$ORT_LIB")"

BGE="$WORK/bge-m3"; mkdir -p "$BGE"; HF="https://huggingface.co/BAAI/bge-m3/resolve/${BGE_COMMIT}"
curl -fsSL --retry 5 "$HF/onnx/model.onnx"             -o "$BGE/model.onnx"             || fail hf_model
curl -fsSL --retry 5 "$HF/onnx/model.onnx_data"        -o "$BGE/model.onnx_data"        || fail hf_data
curl -fsSL --retry 5 "$HF/onnx/Constant_7_attr__value" -o "$BGE/Constant_7_attr__value" || fail hf_const
curl -fsSL --retry 5 "$HF/tokenizer.json"              -o "$BGE/tokenizer.json"         || fail hf_tok
say "model_data_bytes=$(stat -c %s "$BGE/model.onnx_data")"

export CGO_ENABLED=1
export ONNXRUNTIME_SHARED_LIBRARY_PATH="$ORT_LIB"
export LD_LIBRARY_PATH="$ORT_DIR:${LD_LIBRARY_PATH:-}"
go build -tags onnx -o "$WORK/embed" ./cmd/embed >/tmp/build.txt 2>&1 || fail build "$(tail -2 /tmp/build.txt | tr '\n' ' ')"
say "build=ok"

# ---- EMBED (recent-first, resumable, time-bounded) --------------------------
export BGE_M3_DIR="$BGE" ORT_EP="$EP" EMBED_GRAPH_OPT=1
REPORT="$WORK/embed-report.json"; START=$(date +%s)
# --no-hnsw: this is a per-series sub-base; the HNSW is built once post-merge.
# `timeout` lets the campaign honour the 12h cap and still reach the version tail.
timeout "${TIME_BUDGET}" "$WORK/embed" --db "$EMBEDDED_DB" \
  --embed-floor "$FLOOR" --series "$SERIES" --order recent --resume \
  --checkpoint-every "$CHECKPOINT_EVERY" --no-hnsw \
  --require-semantic --report json >"$REPORT" 2>/tmp/embed.err
RC=$?; ELAPSED=$(( $(date +%s) - START ))
if [ "$RC" = "124" ]; then
  say "embed=timeout elapsed=${ELAPSED}s (hit time budget — partial DB is resumable next run)"
elif [ "$RC" != "0" ]; then
  say "embed_rc=$RC err=$(tail -2 /tmp/embed.err | tr '\n' ' ' | cut -c1-200)"; fail embed_run
fi
MODEL=$(grep -oE '"model"[^,]*' "$REPORT" | head -1 | sed -E 's/.*: *"?//; s/"//')
EMB=$(grep -oE '"embedded_clauses": *[0-9]+' "$REPORT" | grep -oE '[0-9]+')
NUL=$(grep -oE '"null_embeddings_at_floor": *[0-9]+' "$REPORT" | grep -oE '[0-9]+')
say "model=$MODEL embedded=${EMB:-0} null_at_floor=${NUL:-?} elapsed=${ELAPSED}s ep=$EP rc=$RC"
awk -v e="${EMB:-0}" -v t="$ELAPSED" 'BEGIN{if(t>0)printf "RESULT throughput=%.2f clauses_per_s_%s\n",e/t,"'"$EP"'"}' | tee -a "$R"

# ---- self-version the partial DB back to the resume dataset ------------------
# Only when creds are injected (kernel Secrets) AND something was embedded — avoids
# spamming empty versions. The driver can also pull `kernels output` and version
# from the laptop instead (creds-never-leave-laptop posture); this tail is the
# opt-in for fully-unattended CI runs.
EMBED_STATE_DS="${EMBED_STATE_DS:-${KAGGLE_USERNAME:-}/3gpp-embedded-s${SERIES}}"
if [ -n "${KAGGLE_USERNAME:-}" ] && [ -n "${KAGGLE_KEY:-}" ] && [ "${EMB:-0}" -ge 1 ]; then
  if command -v kaggle >/dev/null 2>&1 || pip install --quiet kaggle 2>/dev/null; then
    OUT="$WORK/state"; mkdir -p "$OUT"; cp "$EMBEDDED_DB" "$OUT/3gpp-embedded.duckdb"
    printf '{\n "title": "3gpp-embedded-s%s",\n "id": "%s",\n "licenses": [{"name": "CC0-1.0"}]\n}\n' \
      "$SERIES" "$EMBED_STATE_DS" > "$OUT/dataset-metadata.json"
    if kaggle datasets version -p "$OUT" -m "series=$SERIES embedded=$EMB null=$NUL" --dir-mode zip >/tmp/ver.txt 2>&1; then
      say "versioned=$EMBED_STATE_DS"
    else
      kaggle datasets create -p "$OUT" --dir-mode zip >/tmp/cre.txt 2>&1 \
        && say "created=$EMBED_STATE_DS" \
        || say "version=skip detail=$(tail -1 /tmp/ver.txt | cut -c1-120)"
    fi
  else
    say "version=skip detail=no_kaggle_cli"
  fi
else
  say "version=skip detail=no_creds_or_nothing_embedded embedded=${EMB:-0}"
fi

# A complete series ends with null_at_floor==0 AND no timeout. A timeout/partial is
# a SUCCESSFUL increment — the driver re-launches to continue.
if [ "$RC" = "0" ] && [ "${NUL:-1}" = "0" ] && [ "${EMB:-0}" -ge 0 ]; then
  say "step=OK complete=1"
else
  say "step=OK complete=0 (partial — resume next run)"
fi
