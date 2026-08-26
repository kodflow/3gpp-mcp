#!/usr/bin/env bash
#
# lib-local.sh — helpers partagés par le pipeline d'indexation LOCAL.
#
# Le pipeline local remplace les 5 workflows GitHub + les 2 campagnes Kaggle par
# une seule chaîne rejouable sur le poste. Principe directeur : ne PAS réinventer
# de couche de reprise, mais s'appuyer sur l'idempotence déjà présente dans les
# outils, et n'ajouter un stamp que là où il n'y en a pas.
#
#   corpus.sh      incrémental nativement (ne retélécharge/reconvertit pas)
#   ingest         --resume via la table ingest_log (stampée PIPELINE_VERSION)
#   embed-io       --export-worklist ne sort que les clauses `embedding IS NULL`
#   embedder       --resume-from <ledger> (répétable) → reprise par chunk_id ET par
#                  content-hash : c'est le pivot de la dédup inter-release
#   merge/freeze   pas d'idempotence propre → stamps explicites (voir stamp/stamped)
#
# shellcheck shell=bash

set -uo pipefail

LOCAL_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
export LOCAL_ROOT

# ---------------------------------------------------------------- arborescence
#
# PERFORMANCE WSL2 : les I/O sur /mnt/c passent par 9p/drvfs et sont ~10x plus
# lentes que l'ext4 natif. Un build cargo, ou un corpus de ~37 Go de petits
# fichiers HTML, y devient pénible. Si le repo est monté depuis Windows et que
# l'utilisateur n'impose rien, on bascule data/ et target/ sur le disque WSL.
# Override explicite : DATA=… CARGO_TARGET_DIR=… (ou WSL_WORK=… pour déplacer).
if [ -z "${DATA:-}" ] && [ -z "${CARGO_TARGET_DIR:-}" ]; then
  case "$LOCAL_ROOT" in
    /mnt/*)
      _wsl_work="${WSL_WORK:-$HOME/3gpp-work}"
      mkdir -p "$_wsl_work"
      DATA="$_wsl_work/data"
      CARGO_TARGET_DIR="$_wsl_work/target"
      printf '\033[2m   [local] repo sur /mnt/c → data/ et target/ bascules sur %s (ext4 natif)\033[0m\n' "$_wsl_work" >&2
      ;;
  esac
fi
DATA="${DATA:-$LOCAL_ROOT/data}"
export CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-$LOCAL_ROOT/rust/target}"
RUST_BIN="$CARGO_TARGET_DIR/release"

SRC_ORIGIN="$DATA/sources/origin"
SRC_CONVERT="$DATA/sources/convert"
LOCAL_DIR="$DATA/local"
STATE_DIR="$LOCAL_DIR/state"
SHARD_DIR="$LOCAL_DIR/shards"
VEC_DIR="$LOCAL_DIR/vecs"
LOG_DIR="$LOCAL_DIR/logs"

# LEDGER GLOBAL content-addressed : LE fichier qui porte la dédup inter-release.
# Chaque ligne = {"chunk_id":N,"hash":"<clause_hash 16 hex>","vec":[…1024 f32]}.
# `clause_hash = sha256(heading + "\n" + text + "|" + embed_identity)[:16]` — pour
# une identité d'embed fixée, c'est un hash de CONTENU pur. En passant ce même
# fichier en --resume-from à CHAQUE shard, toute clause dont le texte a déjà été
# vu — n'importe quelle release, n'importe quelle série — est remplie sans GPU.
# Mesuré sur le corpus réel (2 855 712 clauses) : 2 282 337 embeddables pour
# 833 924 textes distincts → facteur 2,74×, dont 79,8 % de doublons INTER-release.
VEC_LEDGER="${VEC_LEDGER:-$VEC_DIR/ledger.jsonl}"

# Index de corpus : écrit par `merge --index-out`, relu par `discover --index`.
# C'est ce qui referme la boucle du delta. En CI il n'était plus republié depuis
# le nettoyage de 2026-06, donc le delta était ancré sur une photo gelée.
CORPUS_INDEX="$LOCAL_DIR/corpus-index.json"
SUBJECT_INDEX="$LOCAL_DIR/subject-index.json"
BUILD_INDEX="$LOCAL_DIR/build-index.json"
ABSENT_INDEX="$LOCAL_DIR/absent-index.json"

DB_OUT="${DB_OUT:-$DATA/3gpp.duckdb}"
MODEL_DIR="${MODEL_DIR:-$DATA/models/bge-m3}"

mkdir -p "$STATE_DIR" "$SHARD_DIR" "$VEC_DIR" "$LOG_DIR" "$SRC_ORIGIN" "$SRC_CONVERT"

# ------------------------------------------------------------------ log & garde
_c_reset=$'\033[0m'; _c_blue=$'\033[34m'; _c_grn=$'\033[32m'
_c_yel=$'\033[33m';  _c_red=$'\033[31m'; _c_dim=$'\033[2m'
[ -t 2 ] || { _c_reset=""; _c_blue=""; _c_grn=""; _c_yel=""; _c_red=""; _c_dim=""; }

log()  { printf '%s%s [%s]%s %s\n' "$_c_blue" "$(date +%H:%M:%S)" "${PHASE:-local}" "$_c_reset" "$*" >&2; }
ok()   { printf '%s%s [%s] ✓%s %s\n' "$_c_grn" "$(date +%H:%M:%S)" "${PHASE:-local}" "$_c_reset" "$*" >&2; }
warn() { printf '%s%s [%s] ⚠%s %s\n' "$_c_yel" "$(date +%H:%M:%S)" "${PHASE:-local}" "$_c_reset" "$*" >&2; }
die()  { printf '%s%s [%s] ✗%s %s\n' "$_c_red" "$(date +%H:%M:%S)" "${PHASE:-local}" "$_c_reset" "$*" >&2; exit 1; }
dim()  { printf '%s   %s%s\n' "$_c_dim" "$*" "$_c_reset" >&2; }

need() { command -v "$1" >/dev/null 2>&1 || die "outil manquant: $1 — lance 'make local-setup'"; }

# free_gb <path> — espace libre en Go (entier)
free_gb() { df -BG "$1" 2>/dev/null | awk 'NR==2{gsub(/G/,"",$4); print $4+0}'; }

# guard_disk <min_gb> <path> — abandonne proprement plutôt que remplir le disque.
# corpus.sh natif ne s'arrête qu'à 5 Go, ce qui est trop tard quand la conversion
# LibreOffice a besoin de place pour son profil jetable.
guard_disk() {
  local min="$1" path="${2:-$DATA}" have
  have="$(free_gb "$path")"
  [ "${have:-0}" -ge "$min" ] || die "disque insuffisant: ${have}Go libres < ${min}Go requis sur $path"
}

# ------------------------------------------------------------------ ledger d'état
# stamp <kind> <key> <fingerprint> — marque une étape faite pour cette empreinte.
stamp() {
  local kind="$1" key="$2" fp="$3"
  mkdir -p "$STATE_DIR/$kind"
  printf '%s\n' "$fp" > "$STATE_DIR/$kind/${key//\//_}"
}

# stamped <kind> <key> <fingerprint> — 0 si déjà fait AVEC la même empreinte.
stamped() {
  local kind="$1" key="$2" fp="$3" f="$STATE_DIR/$kind/${key//\//_}"
  [ -f "$f" ] && [ "$(cat "$f" 2>/dev/null)" = "$fp" ]
}

unstamp() { rm -f "$STATE_DIR/$1/${2//\//_}"; }

# fp_files <paths…> — empreinte stable d'un ensemble de fichiers (taille+mtime).
# Volontairement PAS un sha256 du contenu : sur ~37 Go de HTML ce serait plus long
# que l'ingestion elle-même, et taille+mtime suffit à détecter une reconversion.
fp_files() {
  find "$@" -type f -printf '%s %T@ %p\n' 2>/dev/null | LC_ALL=C sort | sha256sum | cut -c1-16
}

# ------------------------------------------------------------- identité d'embed
# L'identité d'embed est le digest12 qui verrouille (famille, révision, tokenizer,
# dim, normalisation, précision, windowing, max_tokens). Elle est calculée par le
# Go `cmd/embedid` — le SEUL point où le Go reste dans la boucle d'ÉCRITURE.
# Un changement d'identité invalide tous les vecteurs : c'est voulu.
embed_identity() {
  local id
  id="$(cd "$LOCAL_ROOT" && CGO_ENABLED=1 go run ./cmd/embedid 2>/dev/null | tr -d '[:space:]')"
  [ -n "$id" ] || die "cmd/embedid n'a rien renvoyé (Go+CGO installés ?)"
  printf '%s\n' "$id"
}

# ---------------------------------------------------------------- build Rust/Go
# Les crates embedder / embed-core / discover sont HORS du workspace (ils tirent
# ort/CUDA ou sont des cdylib) : chacun se construit avec son propre manifeste.
cargo_build() {
  local manifest="$1"; shift
  need cargo
  dim "cargo build --release --manifest-path $manifest $*"
  ( cd "$LOCAL_ROOT" && cargo build --release --manifest-path "$manifest" "$@" ) \
    || die "échec du build cargo: $manifest $*"
}

have_gpu() { command -v nvidia-smi >/dev/null 2>&1 && nvidia-smi -L >/dev/null 2>&1; }

# ---------------------------------------------------------------------- divers
# series_of <tag> — "24" ou "24|Rel-19" → "24"
series_of() { printf '%s\n' "${1%%|*}"; }
# release_of <tag> — "24|Rel-19" → "Rel-19" ; "24" → ""
release_of() { case "$1" in *\|*) printf '%s\n' "${1#*|}";; *) printf '\n';; esac; }
# shard_db <tag> — chemin du shard DuckDB
shard_db() { printf '%s/%s.duckdb\n' "$SHARD_DIR" "${1//|/-}"; }

human() { numfmt --to=iec --suffix=B "$1" 2>/dev/null || printf '%s\n' "$1"; }
