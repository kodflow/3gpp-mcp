#!/usr/bin/env bash
# kaggle-auth.sh — pick a working Kaggle account, with fallback.
#
# Two accounts are configured as GitHub secrets/vars. This script tries them in
# order: the FIRST whose auth probe succeeds is selected and its (token, username)
# exported for the rest of the job. If NONE works, it exits non-zero so the CI
# stops — "si les 2 plantent on stop via la CI".
#
# Accounts (from the job env, sourced from GH secrets/vars):
#   primary : KAGGLE_API_TOKEN          + KAGGLE_USERNAME
#   fallback: KAGGLE_API_TOKEN_FALLBACK + KAGGLE_USERNAME_FALLBACK
#
# Switch easily with KAGGLE_ACCOUNT (which account is TRIED FIRST; the other is the
# fallback). Wire it to a workflow_dispatch input so flipping accounts is one click:
#   primary  (default) -> try [primary, fallback]
#   fallback           -> try [fallback, primary]
#
# On success, appends to $GITHUB_ENV (so later steps inherit the SELECTED account):
#   KAGGLE_API_TOKEN=<selected token>     # the Kaggle CLI reads this for auth
#   KAGGLE_USER=<selected username>       # namespaces kernel slugs + resume datasets
#   KAGGLE_ACCOUNT_SELECTED=<primary|fallback>
# It NEVER prints a token to stdout (only usernames + which account won).
#
# Local / unit testing: set KAGGLE_AUTH_PROBE to a stub command whose exit code
# stands in for the real Kaggle call (default: `kaggle kernels list -m -p 1`). The
# stub receives the candidate token in $KAGGLE_API_TOKEN, exactly like the real CLI.
set -euo pipefail

# The auth probe: any cheap authenticated call. Succeeds (rc 0) only with a valid
# token; an expired/invalid token (or a transient API failure) returns non-zero and
# triggers the fallback. Overridable for tests.
PROBE="${KAGGLE_AUTH_PROBE:-kaggle kernels list -m -p 1}"

case "${KAGGLE_ACCOUNT:-primary}" in
  primary)  ORDER="primary fallback" ;;
  fallback) ORDER="fallback primary" ;;
  *) echo "::error::KAGGLE_ACCOUNT='${KAGGLE_ACCOUNT}' invalid (use 'primary' or 'fallback')"; exit 2 ;;
esac

token_of() {
  case "$1" in
    primary)  printf '%s' "${KAGGLE_API_TOKEN:-}" ;;
    fallback) printf '%s' "${KAGGLE_API_TOKEN_FALLBACK:-}" ;;
  esac
}
user_of() {
  case "$1" in
    primary)  printf '%s' "${KAGGLE_USERNAME:-}" ;;
    fallback) printf '%s' "${KAGGLE_USERNAME_FALLBACK:-}" ;;
  esac
}

selected=""
sel_token=""
sel_user=""
for name in $ORDER; do
  token="$(token_of "$name")"
  user="$(user_of "$name")"
  if [ -z "$token" ] || [ -z "$user" ]; then
    echo "kaggle-auth: account '$name' not configured (token or username empty) — skipping"
    continue
  fi
  echo "kaggle-auth: trying account '$name' (user=$user) …"
  if KAGGLE_API_TOKEN="$token" sh -c "$PROBE" >/dev/null 2>&1; then
    echo "kaggle-auth: account '$name' authenticated (user=$user) — selected"
    selected="$name"; sel_token="$token"; sel_user="$user"
    break
  fi
  echo "::warning::kaggle-auth: account '$name' (user=$user) failed auth — falling back"
done

if [ -z "$selected" ]; then
  echo "::error::kaggle-auth: ALL configured Kaggle accounts failed auth — stopping the run"
  exit 1
fi

# Hand the SELECTED account to the rest of the job. Both tokens are registered GH
# secrets, so the value is masked in logs even though it lands in the runner env.
if [ -n "${GITHUB_ENV:-}" ]; then
  {
    echo "KAGGLE_API_TOKEN=$sel_token"
    echo "KAGGLE_USER=$sel_user"
    echo "KAGGLE_ACCOUNT_SELECTED=$selected"
  } >> "$GITHUB_ENV"
fi
if [ -n "${GITHUB_OUTPUT:-}" ]; then
  {
    echo "account=$selected"
    echo "user=$sel_user"
  } >> "$GITHUB_OUTPUT"
fi
echo "kaggle-auth: selected account='$selected' user='$sel_user'"
