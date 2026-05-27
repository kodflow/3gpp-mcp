#!/bin/bash
# ============================================================================
# subagent-stop.sh - Log subagent completion
# Hook: SubagentStop (all agent types)
# Exit 0 = allow parent to continue
#
# Purpose: Track subagent lifecycle for debugging and audit.
# ============================================================================

set +e

INPUT="$(cat 2>/dev/null || true)"
if [ -z "$INPUT" ] || ! command -v jq &>/dev/null; then
    exit 0
fi

# CRITICAL: Prevent infinite loop — exit immediately if stop hook is already active
STOP_ACTIVE=$(printf '%s' "$INPUT" | jq -r '.stop_hook_active // false' 2>/dev/null || echo "false")
if [ "$STOP_ACTIVE" = "true" ]; then
    exit 0
fi

AGENT_TYPE=$(printf '%s' "$INPUT" | jq -r '.agent_type // "unknown"' 2>/dev/null || echo "unknown")
SESSION_ID=$(printf '%s' "$INPUT" | jq -r '.session_id // "unknown"' 2>/dev/null || echo "unknown")
LAST_MSG_LEN=$(printf '%s' "$INPUT" | jq -r '.last_assistant_message // "" | length' 2>/dev/null || echo "0")
TRANSCRIPT=$(printf '%s' "$INPUT" | jq -r '.agent_transcript_path // ""' 2>/dev/null || echo "")
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-/workspace}"
BRANCH=$(git -C "$PROJECT_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "detached")
BRANCH_SAFE=$(printf '%s' "$BRANCH" | tr '/ ' '__')

LOG_DIR="$PROJECT_DIR/.claude/logs/$BRANCH_SAFE"
mkdir -p "$LOG_DIR" 2>/dev/null || true

jq -n -c \
    --arg ts "$TIMESTAMP" \
    --arg sid "$SESSION_ID" \
    --arg agent "$AGENT_TYPE" \
    --arg branch "$BRANCH" \
    --argjson msg_len "$LAST_MSG_LEN" \
    --arg transcript "$TRANSCRIPT" \
    '{timestamp:$ts,session_id:$sid,agent_type:$agent,branch:$branch,last_msg_len:$msg_len,transcript:$transcript,event:"SubagentStop"}' \
    >> "$LOG_DIR/subagents.jsonl" 2>/dev/null || true

exit 0
