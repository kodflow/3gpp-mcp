#!/bin/bash
# ============================================================================
# notification.sh - Container-friendly notification handler
# Hook: Notification (all matchers)
# Exit 0 = always (fail-open, async)
#
# Purpose: Audible + logged notification for external monitoring.
# ============================================================================

set +e  # Fail-open: never block

# Read hook input
INPUT=""
if [ ! -t 0 ]; then
    INPUT=$(cat 2>/dev/null || true)
fi

# Extract all available fields
MESSAGE=""
TITLE=""
NOTIFICATION_TYPE=""
if [ -n "$INPUT" ] && command -v jq &>/dev/null; then
    MESSAGE=$(printf '%s' "$INPUT" | jq -r '.message // ""' 2>/dev/null || true)
    TITLE=$(printf '%s' "$INPUT" | jq -r '.title // ""' 2>/dev/null || true)
    NOTIFICATION_TYPE=$(printf '%s' "$INPUT" | jq -r '.notification_type // ""' 2>/dev/null || true)
fi

# Terminal bell only for interactive notifications (idle prompt, permission prompt)
case "$NOTIFICATION_TYPE" in
    idle_prompt|permission_prompt|"")
        printf '\a'
        ;;
esac

# Append to notification log for external monitoring
PROJECT_DIR="${CLAUDE_PROJECT_DIR:-/workspace}"
LOG_DIR="$PROJECT_DIR/.claude/logs"
mkdir -p "$LOG_DIR" 2>/dev/null || true

TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

if command -v jq &>/dev/null; then
    jq -n -c \
        --arg ts "$TIMESTAMP" \
        --arg msg "$MESSAGE" \
        --arg title "$TITLE" \
        --arg ntype "$NOTIFICATION_TYPE" \
        '{timestamp:$ts,title:$title,message:$msg,notification_type:$ntype}' \
        >> "$LOG_DIR/notification.jsonl" 2>/dev/null || true
fi

exit 0
