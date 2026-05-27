#!/bin/bash
# ============================================================================
# session-end.sh - Log session end with duration and summary
# Hook: SessionEnd (matcher: reason)
# Exit 0 = always (fail-open)
#
# Purpose: Record session end in JSONL for audit trail and context recovery.
# ============================================================================

set +e

printf '\a'  # Terminal bell

INPUT="$(cat 2>/dev/null || true)"
REASON="unknown"
SESSION_ID="unknown"
if command -v jq &>/dev/null && [ -n "$INPUT" ]; then
    REASON=$(printf '%s' "$INPUT" | jq -r '.reason // "unknown"' 2>/dev/null || echo "unknown")
    SESSION_ID=$(printf '%s' "$INPUT" | jq -r '.session_id // "unknown"' 2>/dev/null || echo "unknown")
fi

# Validate reason against known enum values
SECURITY_EVENT=false
case "$REASON" in
    clear|logout|prompt_input_exit|other)
        ;; # Valid known reasons
    bypass_permissions_disabled)
        SECURITY_EVENT=true
        ;;
    *)
        REASON="other"
        ;;
esac

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-/workspace}"
BRANCH=$(git -C "$PROJECT_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "detached")
BRANCH_SAFE=$(printf '%s' "$BRANCH" | tr '/ ' '__')
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

LOG_DIR="$PROJECT_DIR/.claude/logs/$BRANCH_SAFE"
SESSION_LOG="$LOG_DIR/session.jsonl"
SESSIONS_INDEX="$PROJECT_DIR/.claude/logs/sessions.jsonl"

# Count events from this session
TOTAL_EVENTS=0
if [ -f "$SESSION_LOG" ]; then
    TOTAL_EVENTS=$(wc -l < "$SESSION_LOG" 2>/dev/null || echo "0")
    TOTAL_EVENTS="${TOTAL_EVENTS## }"  # Trim leading whitespace (macOS wc)
fi

mkdir -p "$LOG_DIR" "$PROJECT_DIR/.claude/logs" 2>/dev/null || true

if command -v jq &>/dev/null; then
    EVENT=$(jq -n -c \
        --arg ts "$TIMESTAMP" \
        --arg sid "$SESSION_ID" \
        --arg reason "$REASON" \
        --arg branch "$BRANCH" \
        --argjson total "${TOTAL_EVENTS:-0}" \
        --argjson security "$SECURITY_EVENT" \
        '{timestamp:$ts,hook_event_name:"SessionEnd",session_id:$sid,reason:$reason,branch:$branch,total_events:$total,security_event:$security}')

    printf '%s\n' "$EVENT" >> "$SESSION_LOG" 2>/dev/null || true
    printf '%s\n' "$EVENT" >> "$SESSIONS_INDEX" 2>/dev/null || true

    # Log security-relevant events separately
    if [ "$SECURITY_EVENT" = "true" ]; then
        printf '%s\n' "$EVENT" >> "$PROJECT_DIR/.claude/logs/security-events.jsonl" 2>/dev/null || true
        echo "⚠️  Security event: session ended due to $REASON" >&2
    fi
fi

exit 0
