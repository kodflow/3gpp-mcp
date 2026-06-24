#!/usr/bin/env bash
# =============================================================================
#  scripts/ci/kaggle.sh — shared Kaggle GPU dispatch helpers for the *-kaggle.yml
#  workflows (corpus-rust-embed, corpus-sparse, diag-embed-ffi).
# =============================================================================
#  WHY: the two-account fallback loop + the 90s warmup + the bounded status poll +
#  the output-pull retry were copy-pasted across every GPU workflow, so a Kaggle
#  quirk (e.g. the stale-status-after-repush race, or a new terminal-status spelling)
#  had to be fixed in N places — and inevitably wasn't. These functions are the EXACT
#  code those workflows ran inline, lifted verbatim; the only parameter is the poll cap
#  (rust=400, sparse=660, ffi=240). Behaviour is unchanged. Each workflow still stages
#  its OWN kernel.py + kernel-metadata.json and does its OWN post-output handling; only
#  the account/auth/poll/pull boilerplate is shared.
#
#  Contract (env the caller sets before the account loop, same names as before):
#    PRIM_TOK / PRIM_USER  — primary  account token + username
#    FB_TOK   / FB_USER    — fallback account token + username
#  Usage:
#    source scripts/ci/kaggle.sh
#    for acct in $(kaggle_account_order "${KAGGLE_ACCOUNT:-primary}"); do
#      kaggle_use_account "$acct" || { echo "acct $acct not configured"; continue; }
#      kaggle_auth_ok      || { echo "acct $acct auth failed"; continue; }
#      # ... stage kernel dir, then:
#      kaggle kernels push -p "$dir" --accelerator NvidiaTeslaT4
#      kaggle_poll_until_terminal "$slug" 400
#      kaggle_pull_output "$slug" out
#      status="$(bash scripts/kaggle-gpu-check.sh out)"   # gpu vs quota (already shared)
#      [ "$status" = gpu ] && break
#    done
# =============================================================================

# kaggle_account_order ACCOUNT -> echoes the try-order: "primary fallback" by default,
# "fallback primary" when ACCOUNT=fallback.
kaggle_account_order() {
  case "${1:-primary}" in
  fallback) echo "fallback primary" ;;
  *) echo "primary fallback" ;;
  esac
}

# kaggle_use_account ACCT -> export KAGGLE_API_TOKEN + KAGGLE_USER from the PRIM_*/FB_*
# env vars the caller set. Returns non-zero (caller should `continue`) when that account
# is not configured (empty token or username).
kaggle_use_account() {
  case "$1" in
  primary) export KAGGLE_API_TOKEN="${PRIM_TOK:-}" KAGGLE_USER="${PRIM_USER:-}" ;;
  fallback) export KAGGLE_API_TOKEN="${FB_TOK:-}" KAGGLE_USER="${FB_USER:-}" ;;
  esac
  [ -n "${KAGGLE_API_TOKEN:-}" ] && [ -n "${KAGGLE_USER:-}" ]
}

# kaggle_auth_ok -> true if the CURRENT creds authenticate against Kaggle.
kaggle_auth_ok() { kaggle kernels list -m -p 1 >/dev/null 2>&1; }

# kaggle_poll_until_terminal SLUG [MAX_ITERS=400] -> wait 90s for the freshly-pushed
# version to leave the PRIOR version's stale terminal status, then poll (30s cadence,
# bounded by MAX_ITERS) until the kernel reaches a terminal status. Echoes every poll so
# a stuck run is diagnosable. Never fails the step — a poll-cap timeout pulls output anyway.
kaggle_poll_until_terminal() {
  local slug="$1" max="${2:-400}" i st done_poll=""
  echo "waiting 90s for the new kernel version to start…"
  sleep 90
  for i in $(seq 1 "$max"); do
    st="$(kaggle kernels status "$slug" 2>&1 || true)"
    echo "poll $i: $st"
    case "$st" in
    *running* | *RUNNING* | *queued* | *QUEUED*) sleep 30; continue ;;
    *complete* | *COMPLETE*) echo "kernel complete"; done_poll=1; break ;;
    *error* | *ERROR* | *cancel* | *CANCEL* | *fail* | *FAIL* | *"not found"* | *404*)
      echo "::warning::kernel ended: $st"; done_poll=1; break ;;
    *) sleep 30 ;;
    esac
  done
  [ -n "$done_poll" ] || echo "::warning::poll cap reached without a terminal status — pulling output anyway"
}

# kaggle_pull_output SLUG OUTDIR -> pull the kernel output (4x retry), then tail the log
# and RESULT.txt for the CI log.
kaggle_pull_output() {
  local slug="$1" out="$2" a
  mkdir -p "$out"
  for a in 1 2 3 4; do
    kaggle kernels output "$slug" -p "$out" && break
    echo "output pull attempt $a failed — retrying"
    sleep 15
  done
  echo "=== KERNEL LOG (tail) ==="
  tail -n 120 "$out"/*.log 2>/dev/null || echo "(no .log pulled)"
  echo "=== RESULT ==="
  cat "$out/RESULT.txt" 2>/dev/null || echo "(no RESULT.txt — see log above)"
}
