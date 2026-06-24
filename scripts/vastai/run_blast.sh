#!/usr/bin/env bash
# =============================================================================
#  scripts/vastai/run_blast.sh — box-side: embed ALL series in ONE rented 4090 session.
# =============================================================================
#  Loops scripts/vastai/run_embed.sh over every corpus series on the same box, so the
#  lexical base + toolchain are reused across series (no per-series rental). Each iteration
#  appends shard/s<NN>.duckdb.zst; the runner pulls the whole shard/ dir and publishes each
#  with publish-vec.sh. Resume-from-GHCR per series means a re-blast is cheap. On-demand
#  only (the workflow enforces it) so the box is not preempted mid-loop.
# =============================================================================
set -uo pipefail
SRC="${SRC:-/workspace/repo}"
# Same rotation list as corpus-embed-orchestrator.yml (core-network + LI first).
SERIES_ALL="${SERIES_ALL:-23 24 29 38 33 28 36 37 21 22 25 26 27 30 31 32 34 35 41 42 43 44 45 46 48 49 50 51 52 55 03}"
ok=0; fail=0
for s in $SERIES_ALL; do
  echo "::group::blast series $s"
  if SERIES="$s" WORK="/workspace/work-$s" bash "$SRC/scripts/vastai/run_embed.sh"; then
    ok=$((ok + 1))
  else
    fail=$((fail + 1)); echo "::warning::series $s failed — continuing the blast"
  fi
  rm -rf "/workspace/work-$s" # free disk between series (the shard is already in shard/)
  echo "::endgroup::"
done
echo "RESULT step=OK blast_ok=$ok blast_fail=$fail shards=$(ls "$SRC"/shard/*.duckdb.zst 2>/dev/null | wc -l)"
