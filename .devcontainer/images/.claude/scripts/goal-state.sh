#!/usr/bin/env bash
# goal-state.sh — Skills Architecture v1.3 (PR1)
#
# WHY: /do --goal-turn cycles through iterations without re-asking
# clarifying questions. The state file persists iteration count,
# decision history and ownership so a new turn can pick up exactly where
# the previous one left off — without that, every turn would re-prompt.
#
# Storage: .claude/state/goals/<slug>.json (one file per goal).
# Lifecycle: create → update(N) → (completed|abandoned|stale) → gc.
#
# Idempotent: create on an existing slug refuses unless --force.

set -euo pipefail

STATE_DIR="${GOAL_STATE_DIR:-.claude/state/goals}"
STALE_AFTER_HOURS="${GOAL_STALE_AFTER_HOURS:-24}"

iso_now() { date -u +"%Y-%m-%dT%H:%M:%SZ"; }

require_slug() {
  [[ -n "${1:-}" ]] || { echo "missing <slug>" >&2; exit 2; }
}

cmd_create() {
  local slug="$1" plan_path="${2:-}" context_path="${3:-}"
  mkdir -p "$STATE_DIR"
  local file="$STATE_DIR/$slug.json"
  if [[ -e "$file" && "${FORCE:-0}" != "1" ]]; then
    echo "goal $slug already exists; pass FORCE=1 to overwrite" >&2; exit 3
  fi
  jq -n \
    --arg slug "$slug" \
    --arg plan "$plan_path" \
    --arg ctx "$context_path" \
    --arg now "$(iso_now)" \
    --arg sess "${CLAUDE_SESSION_ID:-unknown}" \
    '{schema_version:1, slug:$slug, mode:"GOAL_TURN", status:"active",
      iteration:0, max_iterations:10, last_decision:null,
      last_decision_reason:null, created_at:$now, last_updated_at:$now,
      completed_at:null, owner_session:$sess, plan_sha:null, context_sha:null,
      plan_path:$plan, context_path:$ctx,
      goal_contract_path:(".claude/goals/" + $slug + ".md"),
      sub_objectives_done:[], files_modified_log:[]}' \
    > "$file"
  echo "$file"
}

cmd_read() {
  local slug="$1"
  cat "$STATE_DIR/$slug.json"
}

cmd_update() {
  local slug="$1"; shift
  local file="$STATE_DIR/$slug.json"
  [[ -r "$file" ]] || { echo "goal $slug not found" >&2; exit 4; }
  local tmp; tmp="$(mktemp)"
  local filter='.last_updated_at = $now'
  local jq_args=(--arg now "$(iso_now)")
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --iteration)        filter+=" | .iteration = (\$iter|tonumber)"; jq_args+=(--arg iter "$2"); shift 2 ;;
      --status)           filter+=" | .status = \$status";             jq_args+=(--arg status "$2"); shift 2 ;;
      --decision)         filter+=" | .last_decision = \$d";           jq_args+=(--arg d "$2"); shift 2 ;;
      --decision-reason)  filter+=" | .last_decision_reason = \$r";    jq_args+=(--arg r "$2"); shift 2 ;;
      --append-objective) filter+=" | .sub_objectives_done += [\$o]";  jq_args+=(--arg o "$2"); shift 2 ;;
      --append-file)      filter+=" | .files_modified_log += [\$f]";   jq_args+=(--arg f "$2"); shift 2 ;;
      --completed)        filter+=" | .status = \"completed\" | .completed_at = \$now"; shift ;;
      *) echo "unknown flag: $1" >&2; exit 2 ;;
    esac
  done
  jq "${jq_args[@]}" "$filter" "$file" > "$tmp"
  mv "$tmp" "$file"
  cat "$file"
}

cmd_mark_stale() {
  # WHY: a goal whose owner session is gone shouldn't block parallel
  # work — we mark it stale so /do --goal-turn refuses to resume it.
  local slug="$1"
  local file="$STATE_DIR/$slug.json"
  [[ -r "$file" ]] || exit 0
  local last_iso last_epoch now_epoch threshold
  last_iso="$(jq -r '.last_updated_at' "$file")"
  last_epoch="$(date -u -d "$last_iso" +%s 2>/dev/null || echo 0)"
  now_epoch="$(date -u +%s)"
  threshold=$((STALE_AFTER_HOURS * 3600))
  if [[ "$((now_epoch - last_epoch))" -gt "$threshold" ]]; then
    cmd_update "$slug" --status stale >/dev/null
    echo "stale"
  else
    echo "fresh"
  fi
}

cmd_gc() {
  # Delete state files for status in {completed, abandoned} older than 7 days.
  local now_epoch threshold cutoff
  now_epoch="$(date -u +%s)"
  cutoff=$((now_epoch - 7*24*3600))
  shopt -s nullglob
  for f in "$STATE_DIR"/*.json; do
    local status last_iso last_epoch
    status="$(jq -r '.status' "$f")"
    last_iso="$(jq -r '.last_updated_at' "$f")"
    last_epoch="$(date -u -d "$last_iso" +%s 2>/dev/null || echo "$now_epoch")"
    if { [[ "$status" = "completed" ]] || [[ "$status" = "abandoned" ]]; } \
       && [[ "$last_epoch" -lt "$cutoff" ]]; then
      rm -f "$f"
      echo "gc: $f"
    fi
  done
}

main() {
  local action="${1:-}"
  shift || true
  case "$action" in
    create)     require_slug "${1:-}"; cmd_create "$@" ;;
    read)       require_slug "${1:-}"; cmd_read "$1" ;;
    update)     require_slug "${1:-}"; cmd_update "$@" ;;
    mark-stale) require_slug "${1:-}"; cmd_mark_stale "$1" ;;
    gc)         cmd_gc ;;
    -h|--help|"")
      cat <<USAGE
goal-state.sh <action> <args>

actions:
  create <slug> [plan_path] [context_path]
  read <slug>
  update <slug> [--iteration N] [--status S] [--decision D] [--decision-reason R]
                [--append-objective O] [--append-file F] [--completed]
  mark-stale <slug>
  gc
USAGE
      ;;
    *) echo "unknown action: $action" >&2; exit 2 ;;
  esac
}

main "$@"
