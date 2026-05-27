#!/bin/bash
# ============================================================================
# task-completed.sh - Log task completion
# Hook: TaskCompleted (no matcher, always fires, async)
# Exit 0 = allow completion, Exit 2 = prevent completion
#
# Purpose: Track task lifecycle for audit and debugging.
# ============================================================================

set +e

INPUT="$(cat 2>/dev/null || true)"
if [ -z "$INPUT" ] || ! command -v jq &>/dev/null; then
    exit 0
fi

TASK_ID=$(printf '%s' "$INPUT" | jq -r '.task_id // ""' 2>/dev/null || echo "")
TASK_SUBJECT=$(printf '%s' "$INPUT" | jq -r '.task_subject // ""' 2>/dev/null || echo "")
TASK_DESCRIPTION=$(printf '%s' "$INPUT" | jq -r '.task_description // ""' 2>/dev/null || echo "")
SESSION_ID=$(printf '%s' "$INPUT" | jq -r '.session_id // "unknown"' 2>/dev/null || echo "unknown")
TEAMMATE_NAME=$(printf '%s' "$INPUT" | jq -r '.teammate_name // ""' 2>/dev/null || echo "")
TEAM_NAME=$(printf '%s' "$INPUT" | jq -r '.team_name // ""' 2>/dev/null || echo "")
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-/workspace}"
BRANCH=$(git -C "$PROJECT_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "detached")
BRANCH_SAFE=$(printf '%s' "$BRANCH" | tr '/ ' '__')

LOG_DIR="$PROJECT_DIR/.claude/logs/$BRANCH_SAFE"
mkdir -p "$LOG_DIR" 2>/dev/null || true

# Truncate description to avoid huge log entries
TASK_DESC_SHORT="${TASK_DESCRIPTION:0:500}"

jq -n -c \
    --arg ts "$TIMESTAMP" \
    --arg sid "$SESSION_ID" \
    --arg tid "$TASK_ID" \
    --arg subj "$TASK_SUBJECT" \
    --arg desc "$TASK_DESC_SHORT" \
    --arg teammate "$TEAMMATE_NAME" \
    --arg team "$TEAM_NAME" \
    --arg branch "$BRANCH" \
    '{timestamp:$ts,session_id:$sid,task_id:$tid,subject:$subj,description:$desc,teammate_name:$teammate,team_name:$team,branch:$branch,event:"TaskCompleted"}' \
    >> "$LOG_DIR/tasks.jsonl" 2>/dev/null || true

# V3.1: registry lifecycle transition active → completed
CAP=$(cat "$HOME/.claude/.team-capability" 2>/dev/null || echo NONE)
if [ "$CAP" != "NONE" ] && [ -n "$TEAM_NAME" ] && [ -n "$TASK_ID" ]; then
    REGISTRY="$HOME/.claude/logs/$TEAM_NAME/task-registry.jsonl"
    if [ -f "$REGISTRY" ]; then
        TMP=$(mktemp)
        NOW=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
        (flock -x 200
            jq -c \
                --arg id "$TASK_ID" \
                --arg now "$NOW" \
                'if .id == $id and .status == "active" then .status = "completed" | .completed_at = $now else . end' \
                "$REGISTRY" > "$TMP" 2>/dev/null && mv "$TMP" "$REGISTRY"
        ) 200>"${REGISTRY}.lock"
    fi
fi

exit 0
