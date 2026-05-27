#!/bin/bash
# ============================================================================
# user-prompt-submit.sh - Inject branch/task context into every prompt
# Hook: UserPromptSubmit (no matcher, always fires)
# Exit 0 = allow (never block), Exit 2 = block prompt
#
# Purpose: Ensure Claude always knows which branch/task is active,
#          especially after compaction or session resume.
# ============================================================================

set +e

# Read stdin JSON (contains prompt field)
INPUT="$(cat 2>/dev/null || true)"

PROMPT=""
if [ -n "$INPUT" ] && command -v jq &>/dev/null; then
    PROMPT=$(printf '%s' "$INPUT" | jq -r '.prompt // ""' 2>/dev/null || echo "")
fi

# === Reset stop circuit-breaker on new user prompt ===
# If the user is typing a new prompt, they're not stuck in a loop anymore.
# Extract session_id from hook JSON input (same key as on-stop.sh)
# Sanitize to [A-Za-z0-9_-] to prevent path traversal in /tmp file paths
CB_SESSION_ID="default"
if [ -n "$INPUT" ] && command -v jq &>/dev/null; then
    RAW_SID=$(printf '%s' "$INPUT" | jq -r '.session_id // "default"' 2>/dev/null || echo "default")
    CB_SESSION_ID=$(printf '%s' "$RAW_SID" | tr -cd 'A-Za-z0-9_-')
    [ -z "$CB_SESSION_ID" ] && CB_SESSION_ID="default"
fi
STOP_COUNTER_FILE="/tmp/.claude-stop-counter-${CB_SESSION_ID}"
rm -f "$STOP_COUNTER_FILE" "${STOP_COUNTER_FILE}.lock" 2>/dev/null || true

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-/workspace}"
BRANCH=$(git -C "$PROJECT_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")

# Find most recent active plan
PLAN_CONTEXT=""
PLANS_DIR="$PROJECT_DIR/.claude/plans"
if [ -d "$PLANS_DIR" ]; then
    LATEST_PLAN=$(ls -t "$PLANS_DIR"/*.md 2>/dev/null | head -1)
    if [ -n "$LATEST_PLAN" ]; then
        PLAN_NAME=$(basename "$LATEST_PLAN")
        PLAN_CONTEXT="\\nActive plan: $PLAN_NAME (read it if resuming a task)"
    fi
fi

# Only inject if we have meaningful context
if [ -z "$BRANCH" ] || [ "$BRANCH" = "unknown" ]; then
    exit 0
fi

# Output additionalContext
if command -v jq &>/dev/null; then
    CONTEXT="Current branch: $BRANCH${PLAN_CONTEXT}"
    jq -n -c \
        --arg ctx "$CONTEXT" \
        '{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":$ctx}}' \
        2>/dev/null || true
fi

exit 0
