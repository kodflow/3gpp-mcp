#!/usr/bin/env bash
# route-agent.sh — Skills Architecture v1.3 (PR2a + PR2b)
#
# WHY: keeps skills decoupled from concrete agent choices. A skill says
# "I need a worker for /test phase=e2e on this profile" and the router
# returns a concrete (subagent_type, model, effort, count) dispatch
# decision. Routing rules live in routing-table.jsonl; the model is
# resolved from each agent's own frontmatter so the rule never holds a
# stale model name.
#
# Exit codes (contract):
#   0  dispatch success
#   10 no match — fallback general-purpose was used
#   20 invalid profile JSON
#   21 invalid routing rule (malformed JSONL)
#   22 jq guard evaluation error
#   23 agent_template without expand_from (or vice versa)
#   30 resolved agent's <name>.md frontmatter missing
#   31 resolved agent's model invalid (not haiku/sonnet/opus) OR effort invalid

set -uo pipefail
# WHY: not -e so we can return tailored exit codes per error condition.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
AGENTS_DIR="${CLAUDE_AGENTS_DIR:-$(cd "$SCRIPT_DIR/../agents" 2>/dev/null && pwd || echo "$HOME/.claude/agents")}"
ROUTING_TABLE="${CLAUDE_ROUTING_TABLE:-$AGENTS_DIR/routing-table.jsonl}"
TELEMETRY_LOG="${CLAUDE_ROUTING_TELEMETRY:-$HOME/.claude/logs/router-fallbacks.jsonl}"

SKILL=""
PHASE=""
PROFILE_PATH=""
DRY_RUN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skill)    SKILL="$2"; shift 2 ;;
    --phase)    PHASE="$2"; shift 2 ;;
    --profile)  PROFILE_PATH="$2"; shift 2 ;;
    --dry-run)  DRY_RUN=1; shift ;;
    -h|--help)
      cat <<USAGE
route-agent.sh — resolve a skill+phase+profile to a dispatch decision.

  --skill <name>        e.g. /lint, /refine, /search
  --phase <name>        skill phase identifier
  --profile <path>      path to a JSON profile (output of detect-project.sh)
  --dry-run             explain rule resolution, never side-effect

See routing-table.jsonl for available rules.
USAGE
      exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

[[ -z "$SKILL" || -z "$PHASE" || -z "$PROFILE_PATH" ]] && {
  echo "missing --skill / --phase / --profile" >&2; exit 2;
}

# --- Validate profile JSON ---
if ! PROFILE_JSON="$(cat "$PROFILE_PATH" 2>/dev/null)" \
   || ! echo "$PROFILE_JSON" | jq empty 2>/dev/null; then
  echo "invalid profile JSON at $PROFILE_PATH" >&2
  exit 20
fi

# --- Parse routing-table line-by-line ---
[[ -r "$ROUTING_TABLE" ]] || {
  # No table = always fallback
  emit_fallback "no-routing-table" "$PHASE"
  exit 10
}

emit_fallback() {
  local reason="$1" phase="$2"
  mkdir -p "$(dirname "$TELEMETRY_LOG")"
  jq -n --arg ts "$(date -u +%FT%TZ)" --arg s "$SKILL" --arg p "$phase" --arg r "$reason" \
        '{ts:$ts, skill:$s, phase:$p, reason:$r}' >> "$TELEMETRY_LOG"
  jq -n '{
    subagent_type: "general-purpose",
    resolved_model: "sonnet",
    model_source: "fallback",
    effort: "medium",
    count: 1,
    owned_paths_hint: [],
    matched_rule_id: null,
    fallback_used: true,
    expanded_from_template: false
  } | [.]'
}

# --- Validate each rule ---
LINE_NO=0
while IFS= read -r rule; do
  LINE_NO=$((LINE_NO + 1))
  [[ -z "$rule" ]] && continue
  if ! echo "$rule" | jq empty 2>/dev/null; then
    echo "invalid JSON in routing-table.jsonl line $LINE_NO" >&2
    exit 21
  fi
  # agent_template ↔ expand_from coupling
  has_template=$(echo "$rule" | jq 'has("agent_template")')
  has_expand=$(echo "$rule"   | jq 'has("expand_from")')
  has_agent=$(echo "$rule"    | jq 'has("agent")')
  if [[ "$has_template" = "true" && "$has_expand" != "true" ]] \
     || [[ "$has_expand"  = "true" && "$has_template" != "true" ]] \
     || [[ "$has_agent"   = "true" && "$has_template" = "true" ]] \
     || [[ "$has_agent"   != "true" && "$has_template" != "true" ]]; then
    echo "rule $LINE_NO: must have exactly one of (agent) or (agent_template + expand_from)" >&2
    exit 23
  fi
done < "$ROUTING_TABLE"

# --- Filter + score rules ---
match_skill_phase() {
  jq -c --arg s "$SKILL" --arg p "$PHASE" \
     'select((.skill == $s or .skill == "*") and (.phase == $p or .phase == "*"))' \
     "$ROUTING_TABLE"
}

filtered=$(match_skill_phase)

# Apply guards (jq expressions evaluated against profile JSON)
applicable="[]"
while IFS= read -r rule; do
  [[ -z "$rule" ]] && continue
  guard=$(echo "$rule" | jq -r '.guard // "true"')
  if eval_result=$(echo "$PROFILE_JSON" | jq -e "$guard" 2>/dev/null); then
    applicable=$(echo "$applicable" | jq --argjson r "$rule" '. + [$r]')
  else
    # WHY: guard returning false is normal; only a parse error is escalated.
    if echo "$PROFILE_JSON" | jq "$guard" 2>&1 | grep -q "error:"; then
      echo "jq guard error in rule $(echo "$rule" | jq -r '.id'): $guard" >&2
      exit 22
    fi
  fi
done <<< "$filtered"

count=$(echo "$applicable" | jq 'length')
if [[ "$count" -eq 0 ]]; then
  emit_fallback "no-match" "$PHASE"
  exit 10
fi

# Sort by priority DESC, tie-break by id ASC, keep top tier
top_priority=$(echo "$applicable" | jq 'map(.priority // 0) | max')
top_rules=$(echo "$applicable" | jq --argjson p "$top_priority" \
              '[.[] | select((.priority // 0) == $p)] | sort_by(.id)')
top_count=$(echo "$top_rules" | jq 'length')

# --- Resolve agent frontmatter ---
resolve_agent_meta() {
  local agent="$1"
  local file="$AGENTS_DIR/$agent.md"
  [[ -r "$file" ]] || { echo "absent"; return 30; }
  local fm
  fm=$(awk '/^---$/{c++; next} c==1{print}' "$file")
  local model effort
  model=$(echo "$fm" | grep -E '^model:' | head -1 | awk '{print $2}')
  effort=$(echo "$fm" | grep -E '^effort:' | head -1 | awk '{print $2}')
  case "$model" in haiku|sonnet|opus) ;; *) echo "invalid"; return 31 ;; esac
  case "$effort" in low|medium|high|xhigh|max|"") ;; *) echo "invalid-effort"; return 31 ;; esac
  echo "${model}:${effort:-medium}"
}

# Build dispatch entries
entries="[]"
fanout=$(echo "$top_rules" | jq '.[0].fanout // false')

emit_entry() {
  local rule="$1" subagent="$2" template_expanded="$3"
  local meta model_resolved effort
  meta=$(resolve_agent_meta "$subagent") || {
    local rc=$?
    [[ $rc -eq 30 ]] && exit 30
    [[ $rc -eq 31 ]] && exit 31
    exit 30
  }
  model_resolved=${meta%%:*}
  effort_resolved=${meta##*:}
  jq -n \
    --arg s "$subagent" \
    --arg m "$model_resolved" \
    --arg e "${effort_resolved:-medium}" \
    --arg rid "$(echo "$rule" | jq -r '.id')" \
    --argjson expanded "$template_expanded" \
    '{
      subagent_type: $s,
      resolved_model: $m,
      model_source: "agent_frontmatter",
      effort: $e,
      count: 1,
      owned_paths_hint: [],
      matched_rule_id: $rid,
      fallback_used: false,
      expanded_from_template: $expanded
    }'
}

# WHY: emit_entry runs inside $(…). `exit 30` from a subshell only kills
# the subshell — the parent script keeps going with $?=30 but never sees
# it. We re-check $? after every call site and propagate the failure
# explicitly. Without this, the test
# TestRouterRejectsExpandedAgentWithoutFrontmatter never reaches the
# expected exit code (CI failure on PR #370).
propagate_emit_failure() {
  local rc=$1
  if [[ $rc -ge 20 ]]; then
    exit "$rc"
  fi
}

if [[ "$fanout" = "true" ]]; then
  # Emit all top-tier rules; expand templates if present
  while IFS= read -r rule; do
    [[ -z "$rule" ]] && continue
    if echo "$rule" | jq -e '.agent_template' >/dev/null 2>&1; then
      template=$(echo "$rule" | jq -r '.agent_template')
      expand_jq=$(echo "$rule" | jq -r '.expand_from')
      mapfile -t values < <(echo "$PROFILE_JSON" | jq -r "$expand_jq" 2>/dev/null)
      for v in "${values[@]}"; do
        [[ -z "$v" || "$v" = "null" ]] && continue
        agent=$(echo "$template" | sed -e "s/{language}/$v/" -e "s/{aspect}/$v/" -e "s/{framework}/$v/" -e "s/{cloud}/$v/")
        entry=$(emit_entry "$rule" "$agent" "true")
        propagate_emit_failure $?
        entries=$(echo "$entries" | jq --argjson e "$entry" '. + [$e]')
      done
    else
      agent=$(echo "$rule" | jq -r '.agent')
      entry=$(emit_entry "$rule" "$agent" "false")
      propagate_emit_failure $?
      entries=$(echo "$entries" | jq --argjson e "$entry" '. + [$e]')
    fi
  done < <(echo "$top_rules" | jq -c '.[]')
else
  # Emit only the lex-first rule of the top priority tier
  rule=$(echo "$top_rules" | jq -c '.[0]')
  agent=$(echo "$rule" | jq -r '.agent')
  entry=$(emit_entry "$rule" "$agent" "false")
  propagate_emit_failure $?
  entries=$(echo "$entries" | jq --argjson e "$entry" '. + [$e]')
fi

if [[ "$DRY_RUN" -eq 1 ]]; then
  jq -n --argjson e "$entries" --argjson r "$top_rules" \
        '{matched_rules: $r, dispatch: $e}'
else
  echo "$entries"
fi
