#!/usr/bin/env bash
#
# retry.sh — ONE retry policy for every flaky/network operation in the pipeline.
#
# Sourced by scripts/*.sh and by GitHub Actions `run:` steps so that the whole
# corpus pipeline (download → diff → reindex → embed → publish) has a single,
# consistent backoff behaviour instead of ad-hoc per-step loops. Transient
# failures we actually hit in CI — Docker Hub timeouts pulling buildkit, GHCR
# push/pull resets, oras blob 5xx, 3GPP FTP hiccups, apt mirror flaps,
# HuggingFace CDN drops — are retried; a genuinely broken command still fails
# loudly once the attempts are exhausted (we never swallow the final error).
#
# Usage:
#   source "$(dirname "$0")/lib/retry.sh"          # from a script
#   source scripts/lib/retry.sh                     # from a workflow run: step
#
#   retry curl -fsSL "$url" -o out.bin              # command form (preferred)
#   retry docker pull "$img"
#   retry bash -c 'curl -fsSL "$u" | tar -xz -C d'  # pipeline form (wrap in -c)
#
# The command runs at most RETRY_MAX times. Between attempts it sleeps
#   min(RETRY_MAX_DELAY, RETRY_BASE * 2^(n-1)) + jitter[0..RETRY_BASE)
# (exponential backoff with jitter so parallel shards don't synchronise). On
# every failed attempt it prints a GitHub `::warning::` annotation (harmless
# noise when run outside Actions). retry returns the LAST command's exit code,
# so callers keep `set -e` semantics: `retry foo || { echo fail; exit 1; }`.
#
# Tunables (env, all optional):
#   RETRY_MAX        total attempts            (default 5)
#   RETRY_BASE       base backoff seconds      (default 3)
#   RETRY_MAX_DELAY  cap per-sleep seconds     (default 60)
#   RETRY_LABEL      label shown in warnings   (default: the command itself)
#
# Idempotency is the caller's job: every operation wrapped here must be safe to
# repeat (network fetches, pushes, pulls are; a non-idempotent mutation is not).

# Guard against double-sourcing (functions are cheap, but keep it clean). This
# file is meant to be sourced, so `return` is valid; silence shellcheck's
# "unreachable" info which assumes standalone execution.
# shellcheck disable=SC2317
if [ -n "${_RTK_RETRY_SH_LOADED:-}" ]; then return 0; fi
_RTK_RETRY_SH_LOADED=1

# retry <cmd> [args...] — run cmd with exponential backoff + jitter.
retry() {
  local max="${RETRY_MAX:-5}"
  local base="${RETRY_BASE:-3}"
  local cap="${RETRY_MAX_DELAY:-60}"
  local label="${RETRY_LABEL:-$*}"
  local attempt=1 rc=0 delay jitter

  while :; do
    # Run in the current shell so exported funcs / env are visible. The `&& return`
    # short-circuit keeps the real exit code in $? on failure (an `if cmd; then`
    # would reset $? to 0) and is exempt from `set -e` (left side of an AND list).
    "$@" && return 0
    rc=$?
    if [ "$attempt" -ge "$max" ]; then
      echo "::error::retry: '${label}' failed after ${max} attempts (last rc=${rc})" >&2
      return "$rc"
    fi
    # delay = min(cap, base * 2^(attempt-1)) + jitter[0..base)
    delay=$(( base * (1 << (attempt - 1)) ))
    [ "$delay" -gt "$cap" ] && delay="$cap"
    jitter=$(( ${RANDOM:-0} % (base + 1) ))
    delay=$(( delay + jitter ))
    echo "::warning::retry: '${label}' attempt ${attempt}/${max} failed (rc=${rc}); sleeping ${delay}s" >&2
    sleep "$delay"
    attempt=$(( attempt + 1 ))
  done
}

# Export so `bash -c` subshells and `xargs`-spawned shells can use it too.
export -f retry 2>/dev/null || true
