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
#
# Matched with `case`, NOT `printf '%s' "$blob" | grep -q`: under `set -o pipefail`
# that pipeline misreads a SUCCESSFUL GPU run as quota. grep -q exits on the first
# match and closes the pipe; printf, still writing a LARGE blob (the full kernel log
# > the 64 KiB pipe buffer), then takes SIGPIPE (exit 141), and pipefail propagates
# that 141 as the pipeline status — so the `if` is false even though grep matched.
# Small test fixtures fit the pipe buffer so printf never blocked, hiding the bug
# until a real multi-hundred-KB log hit it (Kaggle run 27798943323 threw away a GPU
# run that wrote 400 vectors). `case` does the match in-shell: no pipe, no SIGPIPE.
case "$blob" in
*gpu=present*)
  echo gpu
  exit 0
  ;;
esac

# Otherwise: explicit no-GPU marker, a quota/usage-limit keyword, or no output at all
# (whitespace-only blob). Any of these means the account did not get a GPU → fall back.
# The keyword scan uses a here-string (`<<<`), not a printf pipe, for the same reason.
if [ -z "${blob//[[:space:]]/}" ] ||
  grep -qiE 'gpu=absent|quota|usage limit|exceeded[^.]*gpu|no gpu( available)?|gpuquota' <<<"$blob"; then
  echo quota
  exit 0
fi

# Output exists but carries neither a present- nor absent-GPU marker (unexpected:
# the kernel always logs one). Err toward the fallback rather than silently stalling.
echo quota
