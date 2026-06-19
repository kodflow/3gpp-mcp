#!/usr/bin/env bash
# kaggle-gpu-check.sh — did a pulled Kaggle kernel run actually GET A GPU?
#
# The kernel logs `RESULT gpu=present …` or `RESULT gpu=absent …` at startup
# (scripts/kaggle/kernel-rust-embed.py), so its committed output tells us whether a
# GPU was allocated for the account that ran it. This drives the CI's automatic
# fallback: if an account is out of weekly GPU quota, it gets no GPU → switch accounts.
#
# Prints exactly one word for the given output directory:
#   gpu    — a GPU was allocated (`gpu=present`). Whether the run then succeeded,
#            partially-resumed, or errored is the caller's concern — NOT a quota issue.
#   quota  — NO GPU for this account: `gpu=absent` (CPU fallback), or a quota/usage
#            -limit keyword, or no usable output at all (quota exhaustion often leaves
#            the session with nothing committed / a 404 on output). Caller falls back.
#
# Usage: kaggle-gpu-check.sh <output_dir>
set -euo pipefail
dir="${1:?usage: kaggle-gpu-check.sh <output_dir>}"

# Concatenate RESULT.txt + any *.log (the kernel's RESULT lines are also printed to
# stdout, so they land in the log even when RESULT.txt was not committed/pulled).
blob="$( { cat "$dir/RESULT.txt" 2>/dev/null; cat "$dir"/*.log 2>/dev/null; } || true )"

# A GPU was demonstrably present → this account has quota; do NOT fall back.
if printf '%s' "$blob" | grep -q 'gpu=present'; then
  echo gpu
  exit 0
fi

# Otherwise: explicit no-GPU marker, a quota/usage-limit keyword, or no output at all
# (whitespace-only blob). Any of these means the account did not get a GPU → fall back.
if [ -z "${blob//[[:space:]]/}" ] ||
  printf '%s' "$blob" | grep -q 'gpu=absent' ||
  printf '%s' "$blob" | grep -qiE 'quota|usage limit|exceeded[^.]*gpu|no gpu( available)?|gpuquota'; then
  echo quota
  exit 0
fi

# Output exists but carries neither a present- nor absent-GPU marker (unexpected:
# the kernel always logs one). Err toward the fallback rather than silently stalling.
echo quota
