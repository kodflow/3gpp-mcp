#!/usr/bin/env bash
# Offline test for scripts/kaggle-gpu-check.sh — builds fixture output dirs and
# asserts the gpu/quota verdict. Run: bash scripts/kaggle-gpu-check_test.sh
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/kaggle-gpu-check.sh"
fails=0

check() { # <label> <expected> <result.txt content | "">  [log content]
  label="$1"; expect="$2"; result="$3"; log="${4:-}"
  d="$(mktemp -d)"
  [ -n "$result" ] && printf '%s\n' "$result" >"$d/RESULT.txt"
  [ -n "$log" ] && printf '%s\n' "$log" >"$d/kernel.log"
  got="$(bash "$SCRIPT" "$d")"
  rm -rf "$d"
  if [ "$got" = "$expect" ]; then
    echo "PASS  $label ($got)"
  else
    echo "FAIL  $label (got $got, want $expect)"
    fails=$((fails + 1))
  fi
}

check "gpu present + step OK"        gpu   "RESULT gpu=present detail=Tesla T4
RESULT step=OK complete=0"
check "gpu present but kernel error" gpu   "RESULT gpu=present detail=Tesla T4
RESULT step=FAIL code=slice"
check "gpu present only in log"      gpu   ""  "RESULT gpu=present detail=Tesla T4"
check "gpu absent (CPU fallback)"    quota "RESULT gpu=absent (CPU fallback — ort uses the CPU EP)"
check "no output at all"             quota ""
check "quota keyword in log"         quota ""  "Error: You have exceeded your GPU quota for this week"
check "empty whitespace output"      quota "   "

# Regression: a LARGE log (> the 64 KiB pipe buffer) with gpu=present must still read
# `gpu`. The old `printf | grep -q` took SIGPIPE on the big write and misclassified it
# as quota (Kaggle run 27798943323 discarded a successful GPU run). Build a ~512 KiB log.
big_log_check() {
  d="$(mktemp -d)"
  {
    echo "RESULT gpu=present detail=Tesla T4"
    # ~512 KiB of trailing log AFTER the marker, so grep -q matches early and any
    # writer of the full blob would block/SIGPIPE on the remainder.
    for _ in $(seq 1 8000); do
      echo "RESULT progress line padding to overflow the pipe buffer xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
    done
  } >"$d/kernel.log"
  got="$(bash "$SCRIPT" "$d")"
  rm -rf "$d"
  if [ "$got" = gpu ]; then
    echo "PASS  large log (>64KiB) with gpu=present (gpu)"
  else
    echo "FAIL  large log (>64KiB) with gpu=present (got $got, want gpu) — SIGPIPE regression"
    fails=$((fails + 1))
  fi
}
big_log_check

if [ "$fails" -ne 0 ]; then
  echo "kaggle-gpu-check_test: $fails failure(s)"
  exit 1
fi
echo "kaggle-gpu-check_test: all scenarios passed"
