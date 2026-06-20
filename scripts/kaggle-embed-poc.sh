#!/usr/bin/env bash
#
# kaggle-embed-poc.sh — LOCAL driver for the Kaggle T4 GPU embed POC (PR-11a).
#
# Proves, entirely from the laptop, that we can vectorise a lexical DuckDB on
# Kaggle's free T4 GPU and pull back a valid semantic DB — using the new
# ORT_EP=cuda execution-provider seam (internal/embed). Kaggle is a best-effort
# MANUAL acceleration path, NOT a CI base (12h ephemeral sessions, throttling,
# ToS); the actual GPU run is operator-driven.
#
# Pipeline (Kaggle public API, no notebook clicking):
#   1. assemble  : stage the lexical DB + BGE-M3 model + CUDA ORT lib + a source
#                  tarball (NO creds, NO data/ corpus) into a dataset dir
#   2. dataset   : kaggle datasets create|version  (push inputs)
#   3. push      : kaggle kernels push             (run kernel-embed.py on T4)
#   4. status    : kaggle kernels status           (poll until complete)
#   5. output    : kaggle kernels output           (pull the embedded 3gpp.duckdb)
#
# Credentials: read by the `kaggle` CLI itself from ~/.kaggle/kaggle.json (chmod
# 600) or the KAGGLE_USERNAME / KAGGLE_KEY env vars. This script NEVER reads,
# copies, prints, commits, or stages them — they must never land in the dataset
# or the kernel inputs. The .tar we build excludes data/, .git, and dotfiles.
#
# Usage:
#   KAGGLE_USER=<handle> scripts/kaggle-embed-poc.sh assemble
#   KAGGLE_USER=<handle> scripts/kaggle-embed-poc.sh dataset
#   KAGGLE_USER=<handle> scripts/kaggle-embed-poc.sh run        # push + poll + pull
#   KAGGLE_USER=<handle> scripts/kaggle-embed-poc.sh all        # every step
#
# Env:
#   KAGGLE_USER   your Kaggle handle (required; used to namespace the slugs)
#   LEX_DB        lexical DuckDB to embed   (default data/3gpp.duckdb)
#   MODEL_DIR     BGE-M3 ONNX + tokenizer   (default data/models/bge-m3)
#   ORT_GPU_LIB   CUDA-enabled libonnxruntime.so (required for assemble)
#   EMBED_FLOOR   release floor to embed    (default Rel-15)
#   STAGE         staging dir               (default .kaggle-poc, gitignored)
set -euo pipefail
echo "::error::RETIRED (Phase 11b): the Go embed path was removed; embed runs in Rust via .github/workflows/corpus-rust-embed-kaggle.yml (kernel-rust-embed.py). This driver is kept for history only." >&2; exit 1

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STAGE="${STAGE:-$ROOT/.kaggle-poc}"
LEX_DB="${LEX_DB:-$ROOT/data/3gpp.duckdb}"
MODEL_DIR="${MODEL_DIR:-$ROOT/data/models/bge-m3}"
EMBED_FLOOR="${EMBED_FLOOR:-Rel-15}"
DATASET_DIR="$STAGE/dataset"
KERNEL_DIR="$STAGE/kernel"

die() { echo "error: $*" >&2; exit 1; }

require_cli() {
  command -v kaggle >/dev/null 2>&1 || die "the 'kaggle' CLI is not installed (pip install kaggle); the operator runs this locally"
  # The CLI resolves creds from ~/.kaggle/kaggle.json or KAGGLE_USERNAME/KAGGLE_KEY.
  # We do NOT touch those files here — just confirm the user handle for slugs.
  [ -n "${KAGGLE_USER:-}" ] || die "set KAGGLE_USER to your Kaggle handle (used only to namespace dataset/kernel slugs)"
}

DATASET_SLUG="3gpp-embed-poc-inputs"
KERNEL_SLUG="3gpp-embed-poc"

# assemble: stage inputs. Explicitly excludes creds and the corpus; the source
# tarball is built with git-archive so only tracked files (never data/, never
# ~/.kaggle) are included.
cmd_assemble() {
  [ -f "$LEX_DB" ] || die "lexical DB not found: $LEX_DB (build one small series first, e.g. ingest --series 33 --release Rel-15)"
  [ -d "$MODEL_DIR" ] || die "BGE-M3 model dir not found: $MODEL_DIR (scripts/fetch-model.sh)"
  [ -n "${ORT_GPU_LIB:-}" ] || die "set ORT_GPU_LIB to the CUDA-enabled libonnxruntime.so matching Kaggle's CUDA"
  [ -f "$ORT_GPU_LIB" ] || die "ORT_GPU_LIB not found: $ORT_GPU_LIB"

  rm -rf "$DATASET_DIR" "$KERNEL_DIR"
  mkdir -p "$DATASET_DIR/bge-m3" "$DATASET_DIR/onnxruntime-gpu/lib" "$KERNEL_DIR"

  echo "[assemble] lexical DB  -> $DATASET_DIR/3gpp.duckdb"
  cp "$LEX_DB" "$DATASET_DIR/3gpp.duckdb"
  echo "[assemble] model       -> $DATASET_DIR/bge-m3/"
  cp -r "$MODEL_DIR"/. "$DATASET_DIR/bge-m3/"
  echo "[assemble] ORT GPU lib -> $DATASET_DIR/onnxruntime-gpu/lib/"
  cp "$ORT_GPU_LIB" "$DATASET_DIR/onnxruntime-gpu/lib/libonnxruntime.so"

  # Source tarball from git (tracked files only — guarantees no data/, no creds).
  echo "[assemble] source.tar  -> $DATASET_DIR/src.tar (git-archive: tracked files only)"
  git -C "$ROOT" archive --format=tar -o "$DATASET_DIR/src.tar" HEAD

  # Copy the kernel body next to its metadata; the kernel is the runnable script.
  cp "$ROOT/scripts/kaggle/kernel-embed.py" "$KERNEL_DIR/kernel-embed.py"

  # dataset metadata (kaggle datasets API).
  cat >"$DATASET_DIR/dataset-metadata.json" <<JSON
{
  "title": "3GPP embed POC inputs",
  "id": "${KAGGLE_USER}/${DATASET_SLUG}",
  "licenses": [{"name": "other"}]
}
JSON

  # kernel metadata: GPU + internet on, the dataset as the only data source.
  cat >"$KERNEL_DIR/kernel-metadata.json" <<JSON
{
  "id": "${KAGGLE_USER}/${KERNEL_SLUG}",
  "title": "3GPP embed POC",
  "code_file": "kernel-embed.py",
  "language": "python",
  "kernel_type": "script",
  "is_private": true,
  "enable_gpu": true,
  "enable_internet": true,
  "dataset_sources": ["${KAGGLE_USER}/${DATASET_SLUG}"],
  "competition_sources": [],
  "kernel_sources": []
}
JSON

  # Defence-in-depth: assert no credential material slipped into the dataset.
  if grep -rIl --include='*.json' -e 'KAGGLE_KEY' -e '"key"' "$DATASET_DIR" 2>/dev/null | grep -qv dataset-metadata.json; then
    die "refusing to proceed: possible credential file staged in $DATASET_DIR"
  fi
  [ ! -e "$DATASET_DIR/kaggle.json" ] || die "kaggle.json must NEVER be in the dataset"
  echo "[assemble] staged under $STAGE (gitignored). No credentials included."
}

cmd_dataset() {
  require_cli
  [ -f "$DATASET_DIR/dataset-metadata.json" ] || die "run 'assemble' first"
  if kaggle datasets status "${KAGGLE_USER}/${DATASET_SLUG}" >/dev/null 2>&1; then
    echo "[dataset] versioning existing dataset"
    kaggle datasets version -p "$DATASET_DIR" -m "embed POC inputs $(date -u +%FT%TZ)" --dir-mode tar
  else
    echo "[dataset] creating dataset"
    kaggle datasets create -p "$DATASET_DIR" --dir-mode tar
  fi
}

cmd_push() {
  require_cli
  [ -f "$KERNEL_DIR/kernel-metadata.json" ] || die "run 'assemble' first"
  echo "[push] kaggle kernels push"
  kaggle kernels push -p "$KERNEL_DIR"
}

cmd_status() {
  require_cli
  echo "[status] polling kernel ${KAGGLE_USER}/${KERNEL_SLUG} (Ctrl-C to stop)"
  while :; do
    out="$(kaggle kernels status "${KAGGLE_USER}/${KERNEL_SLUG}" 2>&1 || true)"
    echo "  $out"
    case "$out" in
      *complete*|*KernelWorkerStatus.COMPLETE*) echo "[status] complete"; return 0 ;;
      *error*|*KernelWorkerStatus.ERROR*|*cancel*) die "kernel ended in error/cancel — see Kaggle logs" ;;
    esac
    sleep 30
  done
}

cmd_output() {
  require_cli
  mkdir -p "$STAGE/output"
  echo "[output] pulling embedded DB -> $STAGE/output/"
  kaggle kernels output "${KAGGLE_USER}/${KERNEL_SLUG}" -p "$STAGE/output"
  if [ -f "$STAGE/output/3gpp.duckdb" ]; then
    echo "[output] got $STAGE/output/3gpp.duckdb — validate locally (vectors present, HNSW frozen, cosine≈1.0 vs CPU ref)"
  else
    echo "[output] note: 3gpp.duckdb not in output yet (kernel may still be writing) — re-run 'output'"
  fi
}

cmd_run() { cmd_push; cmd_status; cmd_output; }
cmd_all() { cmd_assemble; cmd_dataset; cmd_run; }

case "${1:-}" in
  assemble) cmd_assemble ;;
  dataset)  cmd_dataset ;;
  push)     cmd_push ;;
  status)   cmd_status ;;
  output)   cmd_output ;;
  run)      cmd_run ;;
  all)      cmd_all ;;
  *) echo "usage: $0 {assemble|dataset|push|status|output|run|all}" >&2; exit 2 ;;
esac
