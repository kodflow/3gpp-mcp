#!/bin/bash
# ============================================================================
# teammate-idle.sh - Handle teammate idle event
# Hook: TeammateIdle (no matcher, always fires)
# Exit 0 = allow idle, Exit 2 = continue working
#
# Purpose: Notification + logging when a teammate goes idle.
# ============================================================================

set +e

printf '\a'  # Terminal bell

INPUT="$(cat 2>/dev/null || true)"
TEAMMATE=""
SESSION_ID="unknown"
TEAM_NAME=""
if command -v jq &>/dev/null && [ -n "$INPUT" ]; then
    TEAMMATE=$(printf '%s' "$INPUT" | jq -r '.teammate_name // ""' 2>/dev/null || echo "")
    SESSION_ID=$(printf '%s' "$INPUT" | jq -r '.session_id // "unknown"' 2>/dev/null || echo "unknown")
    TEAM_NAME=$(printf '%s' "$INPUT" | jq -r '.team_name // ""' 2>/dev/null || echo "")
fi

if [ -n "$TEAMMATE" ]; then
    echo "Teammate idle: $TEAMMATE" >&2
fi

# JSONL logging for consistency with other hooks
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-/workspace}"
LOG_DIR="$PROJECT_DIR/.claude/logs"
mkdir -p "$LOG_DIR" 2>/dev/null || true

if command -v jq &>/dev/null; then
    TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    jq -n -c \
        --arg ts "$TIMESTAMP" \
        --arg sid "$SESSION_ID" \
        --arg teammate "$TEAMMATE" \
        '{timestamp:$ts,session_id:$sid,teammate_name:$teammate,event:"TeammateIdle"}' \
        >> "$LOG_DIR/teammates.jsonl" 2>/dev/null || true
fi

# V3.1: reject idle if active tasks still pending for this teammate
CAP=$(cat "$HOME/.claude/.team-capability" 2>/dev/null || echo NONE)
if [ "$CAP" != "NONE" ] && [ -n "$TEAMMATE" ] && command -v jq &>/dev/null; then
    REGISTRY="$HOME/.claude/logs/${TEAM_NAME:-default}/task-registry.jsonl"
    if [ -f "$REGISTRY" ]; then
        PENDING=$(jq -s --arg a "$TEAMMATE" \
            '[.[] | select(.status == "active" and .assignee == $a)] | length' \
            "$REGISTRY" 2>/dev/null || echo 0)
        if [ "$PENDING" -gt 0 ]; then
            echo "You still have $PENDING active task(s) — continue working" >&2
            exit 2
        fi
    fi
fi

exit 0
