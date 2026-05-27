#!/bin/bash
# ============================================================================
# config-change.sh - Log configuration changes with escalation detection
# Hook: ConfigChange (matcher: source)
# Exit 0 = allow change, Exit 2 = block (except policy_settings)
#
# Purpose: Track config changes for audit trail. Detect permission escalation.
# ============================================================================

set +e

INPUT="$(cat 2>/dev/null || true)"
if [ -z "$INPUT" ] || ! command -v jq &>/dev/null; then
    exit 0
fi

SOURCE=$(printf '%s' "$INPUT" | jq -r '.source // "unknown"' 2>/dev/null || echo "unknown")
FILE_PATH=$(printf '%s' "$INPUT" | jq -r '.file_path // ""' 2>/dev/null || echo "")
SESSION_ID=$(printf '%s' "$INPUT" | jq -r '.session_id // "unknown"' 2>/dev/null || echo "unknown")
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-/workspace}"
LOG_DIR="$PROJECT_DIR/.claude/logs"
mkdir -p "$LOG_DIR" 2>/dev/null || true

# Detect permission escalation attempts
ESCALATION=false
if [ "$SOURCE" = "project_settings" ] || [ "$SOURCE" = "user_settings" ]; then
    if [ -n "$FILE_PATH" ] && [ -f "$FILE_PATH" ]; then
        if grep -q "bypassPermissions" "$FILE_PATH" 2>/dev/null; then
            ESCALATION=true
            echo "⚠️  Security: bypassPermissions detected in $SOURCE ($FILE_PATH)" >&2
        fi
    fi
fi

jq -n -c \
    --arg ts "$TIMESTAMP" \
    --arg sid "$SESSION_ID" \
    --arg src "$SOURCE" \
    --arg fp "$FILE_PATH" \
    --argjson esc "$ESCALATION" \
    '{timestamp:$ts,session_id:$sid,source:$src,file_path:$fp,escalation:$esc,event:"ConfigChange"}' \
    >> "$LOG_DIR/config-changes.jsonl" 2>/dev/null || true

# Log escalation separately for security audit
if [ "$ESCALATION" = "true" ]; then
    jq -n -c \
        --arg ts "$TIMESTAMP" \
        --arg sid "$SESSION_ID" \
        --arg src "$SOURCE" \
        --arg fp "$FILE_PATH" \
        '{timestamp:$ts,session_id:$sid,source:$src,file_path:$fp,event:"EscalationAttempt"}' \
        >> "$LOG_DIR/security-events.jsonl" 2>/dev/null || true
fi

exit 0
