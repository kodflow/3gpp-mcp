#!/bin/bash
# ============================================================================
# observe.sh - Async tool observation capture for continuous learning
# Hook: PreToolUse + PostToolUse (all tools)
# Exit 0 = always (fail-open - never block Claude)
#
# Purpose: Capture lightweight tool observations for pattern extraction.
# Output: ~/.claude/learning/observations.jsonl
# Auto-rotates at 5MB.
# ============================================================================

set +e  # Fail-open: never block

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Source shared utilities
# shellcheck source=common.sh
[ -f "$SCRIPT_DIR/common.sh" ] && . "$SCRIPT_DIR/common.sh"

# Gate: full profile only
check_hook_profile "full" || exit 0

# Guard: skip if explicitly disabled
[ "${ECC_SKIP_OBSERVE:-0}" = "1" ] && exit 0

# Guard: skip subagent invocations
[ "${CLAUDE_CODE_ENTRYPOINT:-}" = "subagent" ] && exit 0

# Require jq
command -v jq &>/dev/null || exit 0

# Read hook JSON from stdin
INPUT="$(cat 2>/dev/null || true)"
[ -z "$INPUT" ] || [ "$INPUT" = "{}" ] && exit 0

# Extract fields
TOOL_NAME=$(printf '%s' "$INPUT" | jq -r '.tool_name // empty' 2>/dev/null) || exit 0
[ -z "$TOOL_NAME" ] && exit 0

COMMAND=$(printf '%s' "$INPUT" | jq -r '.tool_input.command // .tool_input.file_path // .tool_input.pattern // .tool_input.query // ""' 2>/dev/null)
COMMAND="${COMMAND:0:200}"

SESSION_ID="${CLAUDE_SESSION_ID:-unknown}"
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# Setup learning directory
LEARN_DIR="$HOME/.claude/learning"
mkdir -p "$LEARN_DIR" 2>/dev/null || exit 0
OBS_FILE="$LEARN_DIR/observations.jsonl"

# Auto-rotate if > 5MB
if [ -f "$OBS_FILE" ]; then
    SIZE=$(stat -c%s "$OBS_FILE" 2>/dev/null || stat -f%z "$OBS_FILE" 2>/dev/null || echo 0)
    if [ "$SIZE" -gt 5242880 ] 2>/dev/null; then
        mv "$OBS_FILE" "$LEARN_DIR/observations.$(date +%Y%m%d%H%M%S).jsonl" 2>/dev/null || true
    fi
fi

# Build and append observation
jq -n -c \
    --arg ts "$TIMESTAMP" \
    --arg sid "$SESSION_ID" \
    --arg tool "$TOOL_NAME" \
    --arg cmd "$COMMAND" \
    '{ts:$ts,sid:$sid,tool:$tool,input:$cmd}' \
    >> "$OBS_FILE" 2>/dev/null

exit 0
