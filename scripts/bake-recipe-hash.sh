#!/usr/bin/env bash
# bake-recipe-hash.sh — a stable short hash of the files that define HOW the
# 3gpp-data DuckDB is baked (fusion + compaction + FTS index + FROZEN HNSW), NOT
# its corpus content. It is stamped into the 3gpp-data image as the label
# io.kodflow.3gpp.bake.recipe and re-read by the change-guard in
# corpus-data-image.yml.
#
# WHY: the daily guard skips the ~14 GB rebake when the *source corpus* digest is
# unchanged. But the stale-data-layer incident had a different root cause — the
# bake RECIPE changed (cmd/freeze-hnsw was ADDED to the pipeline) while the corpus
# digest stood still, so the guard kept skipping and prod served a data layer that
# predated freeze-hnsw: vectors present, no frozen HNSW, FTS dropped by compaction.
# Hashing the recipe makes "the way we build the DB changed" a first-class rebake
# trigger, independent of the corpus. See [[project_served_stale_data_layer]].
#
# Output: 16 hex chars on stdout. Deterministic (sorted, content-based). A missing
# file is tolerated (the recipe simply omits it) so a refactor that renames a file
# changes the hash → rebake, which is the safe direction.
set -euo pipefail
cd "$(git rev-parse --show-toplevel 2>/dev/null || echo .)"

# The recipe = everything that decides the indexes/structure of the shipped DB.
files=(
  .github/workflows/corpus-data-image.yml
  Dockerfile.data
  cmd/freeze-hnsw/main.go
  cmd/overlay/main.go
  cmd/merge/main.go
  cmd/validate/main.go
  internal/store/hnsw.go
  internal/store/store.go
  scripts/bake-recipe-hash.sh
)

present=()
for f in "${files[@]}"; do
  [ -f "$f" ] && present+=("$f")
done

# sha256 of the concatenated per-file sha256s (stable across runners). The inner
# `sort` makes it order-independent of the shell glob / array.
LC_ALL=C
sha256sum "${present[@]}" 2>/dev/null | awk '{print $1}' | sort | sha256sum | cut -c1-16
