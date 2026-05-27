#!/bin/bash
# ============================================================================
# post-tool-failure.sh - Log tool failures and provide self-correction guidance
# Hook: PostToolUseFailure (all tools)
# Exit 0 = always (sync — outputs additionalContext for Claude self-correction)
#
# Purpose: Build a failure log and feed targeted remediation back to Claude.
# ============================================================================

set +e

INPUT="$(cat 2>/dev/null || true)"
if [ -z "$INPUT" ] || ! command -v jq &>/dev/null; then
    exit 0
fi

TOOL_NAME=$(printf '%s' "$INPUT" | jq -r '.tool_name // "unknown"' 2>/dev/null || echo "unknown")
SESSION_ID=$(printf '%s' "$INPUT" | jq -r '.session_id // "unknown"' 2>/dev/null || echo "unknown")
ERROR=$(printf '%s' "$INPUT" | jq -r '.error // ""' 2>/dev/null || echo "")
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-/workspace}"
BRANCH=$(git -C "$PROJECT_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "detached")
BRANCH_SAFE=$(printf '%s' "$BRANCH" | tr '/ ' '__')

LOG_DIR="$PROJECT_DIR/.claude/logs/$BRANCH_SAFE"
mkdir -p "$LOG_DIR" 2>/dev/null || true

# Truncate error to avoid huge log entries
ERROR_SHORT="${ERROR:0:500}"

jq -n -c \
    --arg ts "$TIMESTAMP" \
    --arg sid "$SESSION_ID" \
    --arg tool "$TOOL_NAME" \
    --arg err "$ERROR_SHORT" \
    --arg branch "$BRANCH" \
    '{timestamp:$ts,session_id:$sid,tool_name:$tool,error:$err,branch:$branch}' \
    >> "$LOG_DIR/failures.jsonl" 2>/dev/null || true

# Pattern-match common failures and provide targeted remediation
ADVICE=""
case "$ERROR" in
    *"command not found"*|*"not found"*)
        ADVICE="Command not found. Check spelling, install the tool, or use an alternative approach."
        ;;
    *"Permission denied"*|*"permission denied"*)
        ADVICE="Permission denied. Try with appropriate permissions or check file ownership."
        ;;
    *"No such file or directory"*)
        ADVICE="File/directory not found. Verify the path exists using Glob or ls before retrying."
        ;;
    *"FAILED"*|*"FAIL"*|*"AssertionError"*|*"assert"*)
        ADVICE="Test failure detected. Read the test output carefully, fix the code, then re-run."
        ;;
    *"syntax error"*|*"SyntaxError"*)
        ADVICE="Syntax error. Check the file for typos, missing brackets, or incorrect indentation."
        ;;
    *"timeout"*|*"Timeout"*|*"ETIMEDOUT"*)
        ADVICE="Operation timed out. Consider breaking the command into smaller steps."
        ;;
    *"ENOENT"*|*"MODULE_NOT_FOUND"*)
        ADVICE="Module not found. Check package.json/requirements.txt and run install."
        ;;
esac

# Output additionalContext for Claude self-correction
if [ -n "$ADVICE" ]; then
    CONTEXT="Tool '$TOOL_NAME' failed: ${ERROR_SHORT:0:200}. Suggestion: $ADVICE"
    jq -n -c \
        --arg ctx "$CONTEXT" \
        '{"hookSpecificOutput":{"hookEventName":"PostToolUseFailure","additionalContext":$ctx}}' \
        2>/dev/null || true
fi

exit 0
