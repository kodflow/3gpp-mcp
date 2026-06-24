#!/usr/bin/env bash
# =============================================================================
#  scripts/ci/vastai.sh — rent the cheapest reliable RTX 4090 on vast.ai, run a 3GPP
#  embed shard on it, pull the result shard back, and ALWAYS destroy the box.
# =============================================================================
#  Runs on the trusted CI RUNNER (not the rented box). VAST_API_KEY stays here and is
#  NEVER passed into the instance (the host is root). The box gets only a read-only
#  GHCR_READ_PAT to pull the lexical base; it produces a shard that THIS runner pulls
#  back (vastai copy) and a later step publishes with the write PAT.
#
#  Teardown has THREE independent paths so a hung/dead box still dies:
#    1. EXIT/INT/TERM trap (normal + cancelled-runner case);
#    2. a wall-clock watchdog subshell (the only kill that survives a hung ssh/CUDA wedge);
#    3. .github/workflows/vastai-reconcile.yml cron (covers a RUNNER death — trap never fires).
#
#  Env: VAST_API_KEY, GHCR_READ_PAT, GHCR_OWNER, SERIES; optional REF, CEIL_DPH, MAX_HOURS,
#       RUN_SCRIPT (scripts/vastai/run_embed.sh | run_sparse.sh).
# =============================================================================
set -euo pipefail

: "${VAST_API_KEY:?}" "${GHCR_READ_PAT:?}" "${SERIES:?}"
GHCR_OWNER="${GHCR_OWNER:-kodflow}"
REF="${REF:-main}"
REF_SHA="${REF_SHA:-}" # immutable commit the box checks out (the workflow passes github.sha)
IMG="${IMG:-nvidia/cuda:12.4.1-cudnn-runtime-ubuntu22.04}" # glibc 2.35 + cuDNN9 ⇒ ORT 1.20.1 CUDA EP, 4090 = CC 8.9
CEIL_DPH="${CEIL_DPH:-0.55}"                               # hard $/hr ceiling
MAX_HOURS="${MAX_HOURS:-9}"                                # wall-clock kill
RUN_SCRIPT="${RUN_SCRIPT:-scripts/vastai/run_embed.sh}"

command -v vastai >/dev/null 2>&1 || pip install -q "vastai==1.1.2"
vastai set api-key "$VAST_API_KEY" >/dev/null

# vastai_cheapest_4090 → cheapest VERIFIED on-demand single-4090 offer id under the ceiling,
# zero paid-egress. row 1 = header, row 2 = cheapest (sorted ascending by dph_total).
vastai_cheapest_4090() {
  vastai search offers \
    "gpu_name=RTX_4090 num_gpus=1 rentable=true verified=true reliability>0.98 dph_total<${CEIL_DPH} inet_down_cost<0.01 inet_up_cost<0.01" \
    -o dph_total 2>/dev/null | awk 'NR==2{print $1}'
}

# vastai_launch <offer> → echoes instance id. VAST_API_KEY is deliberately NOT in --env.
vastai_launch() {
  local offer="$1" onstart
  # Clone the branch, then pin to the EXACT commit the workflow ran from (immutable —
  # no floating ref on the box). Falls back to the branch tip only if REF_SHA is empty.
  onstart="set -e; exec >>/var/log/onstart.log 2>&1
git clone -b '${REF}' https://github.com/${GHCR_OWNER}/3gpp-mcp /workspace/repo
cd /workspace/repo
[ -n '${REF_SHA}' ] && git checkout --quiet '${REF_SHA}'
bash ${RUN_SCRIPT}
touch /root/onstart-done"
  vastai create instance "$offer" --image "$IMG" --disk 80 --ssh --direct --raw \
    --label "embed-s${SERIES}" \
    --env "-e GHCR_PAT=${GHCR_READ_PAT} -e GHCR_OWNER=${GHCR_OWNER} -e SERIES=${SERIES} -e REF=${REF} -e REF_SHA=${REF_SHA}" \
    --onstart-cmd "$onstart" | jq -r '.new_contract'
}

vastai_status() { vastai show instance "$1" --raw 2>/dev/null | jq -r '.actual_status // "unknown"'; }
vastai_destroy() { vastai destroy instance "$1" >/dev/null 2>&1 || true; }
vastai_assert_gone() {
  vastai show instance "$1" --raw 2>/dev/null | jq -e . >/dev/null 2>&1 &&
    { echo "::error::instance $1 still exists after destroy — run vastai-reconcile"; return 1; } || true
}

main() {
  local off id wd_pid st
  off="$(vastai_cheapest_4090)"
  [ -n "$off" ] || { echo "::error::no on-demand 4090 under \$${CEIL_DPH}/hr"; exit 1; }
  echo "cheapest 4090 offer=$off (ceiling \$${CEIL_DPH}/hr)"
  id="$(vastai_launch "$off")"
  [ -n "$id" ] && [ "$id" != null ] || { echo "::error::launch failed"; exit 1; }
  echo "launched instance=$id"

  # Watchdog: the ONLY teardown that survives a hung ssh/CUDA wedge.
  ( sleep "$((MAX_HOURS * 3600))"; echo "::warning::watchdog ${MAX_HOURS}h — destroying $id"; vastai_destroy "$id" ) &
  wd_pid=$!
  trap 'vastai_destroy "$id"; kill "$wd_pid" 2>/dev/null || true' EXIT INT TERM

  echo "waiting for instance to run…"
  until [ "$(vastai_status "$id")" = running ]; do
    st="$(vastai_status "$id")"
    case "$st" in exited | offline | unknown) echo "::error::instance died early: $st"; exit 1 ;; esac
    sleep 15
  done
  local url; url="$(vastai ssh-url "$id")"
  echo "instance running; waiting for onstart sentinel…"
  until ssh -o StrictHostKeyChecking=no -o ConnectTimeout=20 "$url" 'test -f /root/onstart-done' 2>/dev/null; do
    st="$(vastai_status "$id")"
    case "$st" in exited | offline | unknown) echo "::error::instance died mid-run: $st"; vastai logs "$id" --tail 80 2>/dev/null || true; exit 1 ;; esac
    sleep 30
  done

  echo "onstart done; pulling shard back to the runner…"
  rm -rf ./shard && mkdir -p ./shard
  vastai copy "$id:/workspace/repo/shard" ./shard 2>/dev/null || ssh -o StrictHostKeyChecking=no "$url" 'cd /workspace/repo && tar -c shard' | tar -x
  vastai logs "$id" --tail 60 2>/dev/null || true
  ls -la ./shard/shard 2>/dev/null || ls -la ./shard || true

  vastai_destroy "$id"
  vastai_assert_gone "$id"
  echo "done — shard pulled, instance destroyed"
}
main "$@"
