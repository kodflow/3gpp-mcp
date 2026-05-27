#!/bin/bash
# ============================================================================
# pre-compact.sh - Snapshot state before context compaction
# Hook: PreCompact (matcher: manual|auto)
# Exit 0 = allow compaction to proceed (async)
#
# Purpose: Persist enough state so Claude can resume after compaction.
# ============================================================================

set +e

INPUT="$(cat 2>/dev/null || true)"
TRIGGER="unknown"
SESSION_ID="unknown"
CUSTOM_INSTRUCTIONS=""
if command -v jq &>/dev/null && [ -n "$INPUT" ]; then
    TRIGGER=$(printf '%s' "$INPUT" | jq -r '.trigger // "unknown"' 2>/dev/null || echo "unknown")
    SESSION_ID=$(printf '%s' "$INPUT" | jq -r '.session_id // "unknown"' 2>/dev/null || echo "unknown")
    CUSTOM_INSTRUCTIONS=$(printf '%s' "$INPUT" | jq -r '.custom_instructions // ""' 2>/dev/null || echo "")
fi

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-/workspace}"
BRANCH=$(git -C "$PROJECT_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "detached")
BRANCH_SAFE=$(printf '%s' "$BRANCH" | tr '/ ' '__')
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
SNAPSHOT_TS=$(date -u +"%Y%m%dT%H%M%SZ")

LOG_DIR="$PROJECT_DIR/.claude/logs/$BRANCH_SAFE"
mkdir -p "$LOG_DIR" 2>/dev/null || true

CHECKPOINT="$LOG_DIR/checkpoint.json"
SNAPSHOT="$LOG_DIR/compact-snapshot-$SNAPSHOT_TS.json"

# Build snapshot from checkpoint + current state
CHECKPOINT_DATA="{}"
if [ -f "$CHECKPOINT" ] && command -v jq &>/dev/null; then
    CHECKPOINT_DATA=$(cat "$CHECKPOINT" 2>/dev/null || echo "{}")
fi

if command -v jq &>/dev/null; then
    jq -n \
        --arg ts "$TIMESTAMP" \
        --arg trigger "$TRIGGER" \
        --arg sid "$SESSION_ID" \
        --arg branch "$BRANCH" \
        --arg ci "$CUSTOM_INSTRUCTIONS" \
        --argjson checkpoint "$CHECKPOINT_DATA" \
        '{
            snapshot_time: $ts,
            trigger: $trigger,
            session_id: $sid,
            branch: $branch,
            custom_instructions: $ci,
            checkpoint: $checkpoint,
            recovery_hint: "Compaction occurred. Read this file to restore context."
        }' > "$SNAPSHOT" 2>/dev/null || true

    # Build recovery context
    CONTEXT="Pre-compaction snapshot saved ($TRIGGER, branch: $BRANCH). After compaction, check .claude/plans/ for active tasks."
    if [ -n "$CUSTOM_INSTRUCTIONS" ]; then
        CONTEXT="$CONTEXT Custom instructions preserved in snapshot."
    fi
    jq -n -c \
        --arg ctx "$CONTEXT" \
        '{"hookSpecificOutput":{"hookEventName":"PreCompact","additionalContext":$ctx}}' \
        2>/dev/null || true
fi

exit 0
