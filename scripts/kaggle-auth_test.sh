#!/usr/bin/env bash
# Self-contained test for scripts/kaggle-auth.sh — exercises the account-selection
# and fallback logic with a MOCK probe (KAGGLE_AUTH_PROBE), so it needs no Kaggle
# network access. Run: bash scripts/kaggle-auth_test.sh
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/kaggle-auth.sh"
fails=0

# run <label> <expected_rc> <expected_account> -- <env assignments...>
run() {
  label="$1"; exp_rc="$2"; exp_acct="$3"; shift 3
  genv="$(mktemp)"
  rc=0
  ( export GITHUB_ENV="$genv"; env "$@" bash "$SCRIPT" ) >/dev/null 2>&1 || rc=$?
  acct="$(sed -n 's/^KAGGLE_ACCOUNT_SELECTED=//p' "$genv")"
  rm -f "$genv"
  if [ "$rc" = "$exp_rc" ] && [ "${acct:-none}" = "$exp_acct" ]; then
    echo "PASS  $label (rc=$rc account=${acct:-none})"
  else
    echo "FAIL  $label (rc=$rc want $exp_rc; account=${acct:-none} want $exp_acct)"
    fails=$((fails + 1))
  fi
}

P_PRIM='[ "$KAGGLE_API_TOKEN" = "tprim" ] && exit 0 || exit 1' # only the primary token authenticates
P_FB='[ "$KAGGLE_API_TOKEN" = "tfb" ] && exit 0 || exit 1'     # only the fallback token authenticates
BOTH_CFG='KAGGLE_API_TOKEN=tprim KAGGLE_USERNAME=makingcodes KAGGLE_API_TOKEN_FALLBACK=tfb KAGGLE_USERNAME_FALLBACK=extazy937'

# shellcheck disable=SC2086  # BOTH_CFG is intentionally word-split into env KEY=VAL args
run "primary valid -> primary"               0 primary  KAGGLE_AUTH_PROBE="$P_PRIM" $BOTH_CFG
# shellcheck disable=SC2086
run "primary down, fallback valid -> fallback" 0 fallback KAGGLE_AUTH_PROBE="$P_FB" $BOTH_CFG
# shellcheck disable=SC2086
run "both down -> stop"                       1 none     KAGGLE_AUTH_PROBE='exit 1' $BOTH_CFG
# shellcheck disable=SC2086
run "KAGGLE_ACCOUNT=fallback tries fallback first" 0 fallback KAGGLE_ACCOUNT=fallback KAGGLE_AUTH_PROBE='exit 0' $BOTH_CFG
run "fallback unconfigured, primary valid -> primary" 0 primary \
  KAGGLE_AUTH_PROBE='exit 0' KAGGLE_API_TOKEN=tprim KAGGLE_USERNAME=makingcodes
run "primary unconfigured, fallback valid -> fallback" 0 fallback \
  KAGGLE_AUTH_PROBE='exit 0' KAGGLE_API_TOKEN_FALLBACK=tfb KAGGLE_USERNAME_FALLBACK=extazy937

if [ "$fails" -ne 0 ]; then
  echo "kaggle-auth_test: $fails failure(s)"
  exit 1
fi
echo "kaggle-auth_test: all scenarios passed"
