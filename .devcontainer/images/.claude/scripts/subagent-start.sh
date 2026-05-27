#!/bin/bash
# ============================================================================
# subagent-start.sh - Inject project conventions into subagents
# Hook: SubagentStart (all agent types)
# Exit 0 = allow (inject context)
#
# Purpose: Subagents don't read parent session context; inject critical rules.
# ============================================================================

set +e

INPUT="$(cat 2>/dev/null || true)"
AGENT_TYPE="unknown"
AGENT_ID=""
if command -v jq &>/dev/null && [ -n "$INPUT" ]; then
    AGENT_TYPE=$(printf '%s' "$INPUT" | jq -r '.agent_type // "unknown"' 2>/dev/null || echo "unknown")
    AGENT_ID=$(printf '%s' "$INPUT" | jq -r '.agent_id // ""' 2>/dev/null || echo "")
fi

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-/workspace}"
BRANCH=$(git -C "$PROJECT_DIR" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")

# Audit log subagent start
BRANCH_SAFE=$(printf '%s' "$BRANCH" | tr '/ ' '__')
LOG_DIR="$PROJECT_DIR/.claude/logs/$BRANCH_SAFE"
mkdir -p "$LOG_DIR" 2>/dev/null || true

TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
if command -v jq &>/dev/null; then
    jq -n -c \
        --arg ts "$TIMESTAMP" \
        --arg agent "$AGENT_TYPE" \
        --arg aid "$AGENT_ID" \
        --arg branch "$BRANCH" \
        '{timestamp:$ts,agent_type:$agent,agent_id:$aid,branch:$branch,event:"SubagentStart"}' \
        >> "$LOG_DIR/subagents.jsonl" 2>/dev/null || true
fi

# Inject context for subagent
CONTEXT="## Subagent Context Injection
Branch: $BRANCH | Agent: $AGENT_TYPE
Rules: (1) MCP-FIRST: mcp__github__*, mcp__context7__* before CLI (2) RTK-FIRST: rtk hook compresses Bash output (3) NO AI IN COMMITS (4) ZSH-FIRST: avoid 'for x in \$VAR' (5) Never commit to main directly"

if command -v jq &>/dev/null; then
    jq -n -c \
        --arg ctx "$CONTEXT" \
        '{"hookSpecificOutput":{"hookEventName":"SubagentStart","additionalContext":$ctx}}' \
        2>/dev/null || true
fi

exit 0
