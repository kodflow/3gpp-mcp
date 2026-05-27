#!/bin/bash
# ============================================================================
# task-created.sh - Advisory validation of task-contract v1
# Hook: TaskCreated (no matcher, always fires, sync)
# Exit 0 = allow creation, Exit 2 = reject task
#
# Purpose: parse the task-contract v1 block embedded in task_description,
# validate required fields, check for file ownership collisions, and record
# the task in a local registry for lifecycle tracking.
#
# ADVISORY BY DEFAULT: missing/malformed contract → warning + allow.
# STRICT only on explicit contract violations (empty subject, write collision).
# Reads ONLY stdin + its own registry — never Claude Code internals.
# ============================================================================

set +e

# Gate on capability — early return if teams disabled
CAP=$(cat "$HOME/.claude/.team-capability" 2>/dev/null || echo NONE)
[ "$CAP" = "NONE" ] && exit 0

# Source primitives library
PRIMITIVES="$HOME/.claude/scripts/team-mode-primitives.sh"
if [ -f "$PRIMITIVES" ]; then
    # shellcheck disable=SC1090
    source "$PRIMITIVES"
else
    # Fallback: primitives not installed, advisory mode
    exit 0
fi

INPUT="$(cat 2>/dev/null || true)"
if [ -z "$INPUT" ] || ! command -v jq &>/dev/null; then
    exit 0
fi

SUBJECT=$(printf '%s' "$INPUT"     | jq -r '.task_subject // ""' 2>/dev/null)
DESCRIPTION=$(printf '%s' "$INPUT" | jq -r '.task_description // ""' 2>/dev/null)
# Sanitize team_name to prevent path traversal
TEAM=$(printf '%s' "$INPUT"        | jq -r '.team_name // "default"' 2>/dev/null)
TEAM=$(echo "$TEAM" | tr -cd 'A-Za-z0-9._-' | head -c 64)
TEAM="${TEAM:-default}"
TASK_ID=$(printf '%s' "$INPUT"     | jq -r '.task_id // ""' 2>/dev/null)
TEAMMATE=$(printf '%s' "$INPUT"    | jq -r '.teammate_name // ""' 2>/dev/null)

team_mode_debug_log "task-created: id=$TASK_ID team=$TEAM assignee=$TEAMMATE"

# Minimum sanity check: subject must not be empty
if [ -z "$SUBJECT" ]; then
    echo "Task rejected: subject is empty" >&2
    exit 2
fi

REGISTRY_DIR="$HOME/.claude/logs/$TEAM"
REGISTRY="$REGISTRY_DIR/task-registry.jsonl"
ARCHIVE="$REGISTRY_DIR/task-registry.archive.jsonl"
mkdir -p "$REGISTRY_DIR" 2>/dev/null || true

# Garbage collection: move non-active entries older than 24h to archive
if [ -f "$REGISTRY" ]; then
    CUTOFF=$(epoch_24h_ago)
    TMP=$(mktemp)
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        AGE_ISO=$(printf '%s' "$line" | jq -r '.created_at // "1970-01-01T00:00:00Z"' 2>/dev/null || echo "1970-01-01T00:00:00Z")
        AGE=$(epoch_from_iso "$AGE_ISO")
        STATUS=$(printf '%s' "$line" | jq -r '.status // "unknown"' 2>/dev/null || echo "unknown")
        if [ "$STATUS" = "active" ] || [ "$AGE" -gt "$CUTOFF" ]; then
            printf '%s\n' "$line" >> "$TMP"
        else
            printf '%s\n' "$line" >> "$ARCHIVE"
        fi
    done < "$REGISTRY"
    mv "$TMP" "$REGISTRY"
fi

# Extract task-contract v1 block from description
CONTRACT_JSON=$(extract_task_contract "$DESCRIPTION")

# Idempotency dedup: if the same idempotency_key already exists, skip silently
if [ -n "$CONTRACT_JSON" ] && [ -f "$REGISTRY" ]; then
    IDEM=$(printf '%s' "$CONTRACT_JSON" | jq -r '.idempotency_key // ""' 2>/dev/null)
    if [ -n "$IDEM" ] && grep -q "\"idempotency_key\":\"$IDEM\"" "$REGISTRY" 2>/dev/null; then
        team_mode_debug_log "task-created: dedup by idempotency_key=$IDEM"
        exit 0
    fi
fi

NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# Missing contract block → advisory warning, allow task
if [ -z "$CONTRACT_JSON" ]; then
    echo "Task advisory: no task-contract v1 block in description (recommended for team tasks)" >&2
    jq -nc \
        --arg id "$TASK_ID" \
        --arg team "$TEAM" \
        --arg assignee "$TEAMMATE" \
        --arg subj "$SUBJECT" \
        --arg now "$NOW" \
        '{id:$id, team:$team, assignee:$assignee, subject:$subj, contract:false, access_mode:null, owned_paths:[], status:"active", created_at:$now, completed_at:null, idempotency_key:null}' \
        >> "$REGISTRY" 2>/dev/null || true
    exit 0
fi

# Contract version validation
if ! validate_contract_version "$CONTRACT_JSON"; then
    VER=$(printf '%s' "$CONTRACT_JSON" | jq -r '.contract_version // "unknown"' 2>/dev/null)
    echo "Task advisory: contract_version=$VER not supported (expected 1), advisory mode" >&2
    jq -nc \
        --arg id "$TASK_ID" \
        --arg team "$TEAM" \
        --arg assignee "$TEAMMATE" \
        --arg subj "$SUBJECT" \
        --arg now "$NOW" \
        '{id:$id, team:$team, assignee:$assignee, subject:$subj, contract:false, access_mode:null, owned_paths:[], status:"active", created_at:$now, completed_at:null, idempotency_key:null}' \
        >> "$REGISTRY" 2>/dev/null || true
    exit 0
fi

# Parse contract fields
ACCESS_MODE=$(printf '%s' "$CONTRACT_JSON" | jq -r '.access_mode // "write"' 2>/dev/null)
OWNED=$(printf '%s' "$CONTRACT_JSON" | jq -c '.owned_paths // []' 2>/dev/null)
IDEM=$(printf '%s' "$CONTRACT_JSON" | jq -r '.idempotency_key // ""' 2>/dev/null)
CONTRACT_ASSIGNEE=$(printf '%s' "$CONTRACT_JSON" | jq -r '.assignee // ""' 2>/dev/null)
[ -z "$TEAMMATE" ] && TEAMMATE="$CONTRACT_ASSIGNEE"

# Required-field validation: write mode needs at least one owned path
if [ "$ACCESS_MODE" = "write" ]; then
    OWNED_COUNT=$(printf '%s' "$OWNED" | jq 'length' 2>/dev/null || echo 0)
    if [ "$OWNED_COUNT" -eq 0 ]; then
        echo "Task rejected: access_mode=write requires at least one owned_paths entry" >&2
        exit 2
    fi
fi

# Collision check: active + write + different assignee + overlapping exact paths
if [ "$ACCESS_MODE" = "write" ] && [ -f "$REGISTRY" ]; then
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        OTHER_STATUS=$(printf '%s' "$line"   | jq -r '.status // ""' 2>/dev/null)
        OTHER_MODE=$(printf '%s' "$line"     | jq -r '.access_mode // ""' 2>/dev/null)
        OTHER_ASSIGNEE=$(printf '%s' "$line" | jq -r '.assignee // ""' 2>/dev/null)
        [ "$OTHER_STATUS" = "active" ] || continue
        [ "$OTHER_MODE" = "write" ]    || continue
        [ "$OTHER_ASSIGNEE" != "$TEAMMATE" ] || continue
        OTHER_PATHS=$(printf '%s' "$line" | jq -c '.owned_paths // []' 2>/dev/null)
        OVERLAP=$(jq -nc --argjson a "$OWNED" --argjson b "$OTHER_PATHS" \
            '$a | map(. as $p | $b | index($p)) | map(select(. != null)) | length' 2>/dev/null || echo 0)
        if [ "$OVERLAP" -gt 0 ]; then
            echo "Task rejected: owned_paths collision with active write task '$OTHER_ASSIGNEE'" >&2
            exit 2
        fi
    done < "$REGISTRY"
fi

# Record as active
jq -nc \
    --arg id "$TASK_ID" \
    --arg team "$TEAM" \
    --arg assignee "$TEAMMATE" \
    --arg subj "$SUBJECT" \
    --arg mode "$ACCESS_MODE" \
    --arg idem "$IDEM" \
    --argjson paths "$OWNED" \
    --arg now "$NOW" \
    '{id:$id, team:$team, assignee:$assignee, subject:$subj, contract:true, access_mode:$mode, owned_paths:$paths, status:"active", created_at:$now, completed_at:null, idempotency_key:(if $idem == "" then null else $idem end)}' \
    >> "$REGISTRY" 2>/dev/null || true

team_mode_debug_log "task-created: recorded $TASK_ID status=active mode=$ACCESS_MODE"
exit 0
