#!/bin/bash
# ============================================================================
# worktree-create.sh - Handle worktree creation
# Hook: WorktreeCreate (no matcher, always fires)
# MUST print worktree path to stdout (Claude Code spec requirement)
# Non-zero exit = creation failure
#
# Purpose: Custom worktree setup with logging.
# ============================================================================

set +e

INPUT="$(cat 2>/dev/null || true)"
WORKTREE_NAME=""
if command -v jq &>/dev/null && [ -n "$INPUT" ]; then
    WORKTREE_NAME=$(printf '%s' "$INPUT" | jq -r '.name // ""' 2>/dev/null || echo "")
fi

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-/workspace}"
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# Create worktree in a standard location
WORKTREE_BASE="$HOME/.claude/worktrees"
mkdir -p "$WORKTREE_BASE" 2>/dev/null || true

if [ -n "$WORKTREE_NAME" ]; then
    # Sanitize name: strip path traversal and unsafe characters
    WORKTREE_NAME=$(echo "$WORKTREE_NAME" | tr -cd 'A-Za-z0-9._-' | head -c 64)
    if [ -z "$WORKTREE_NAME" ]; then
        echo "ERROR: worktree name is empty after sanitization" >&2
        exit 1
    fi
    WORKTREE_PATH="$WORKTREE_BASE/$WORKTREE_NAME"

    # === Pre-validation checks ===

    # Auto-remove stale git index lock (common with GitKraken/parallel tools)
    if [ -f "$PROJECT_DIR/.git/index.lock" ]; then
        rm -f "$PROJECT_DIR/.git/index.lock" 2>/dev/null || true
    fi

    # Check if worktree already exists
    if [ -d "$WORKTREE_PATH" ]; then
        echo "ERROR: worktree already exists at $WORKTREE_PATH" >&2
        exit 1
    fi

    # Warn about uncommitted changes (non-blocking)
    DIRTY=$(git -C "$PROJECT_DIR" status --porcelain 2>/dev/null | head -1)
    if [ -n "$DIRTY" ]; then
        echo "WARNING: uncommitted changes in main repo — worktree will be based on last commit" >&2
    fi

    # Fetch latest to ensure base is fresh
    git -C "$PROJECT_DIR" fetch origin 2>/dev/null || true

    # Create the worktree
    git -C "$PROJECT_DIR" worktree add "$WORKTREE_PATH" 2>/dev/null
    RC=$?

    if [ $RC -eq 0 ]; then
        # MUST print path to stdout (Claude Code reads this)
        echo "$WORKTREE_PATH"

        # Log the creation
        LOG_DIR="$PROJECT_DIR/.claude/logs"
        mkdir -p "$LOG_DIR" 2>/dev/null || true
        if command -v jq &>/dev/null; then
            jq -n -c \
                --arg ts "$TIMESTAMP" \
                --arg name "$WORKTREE_NAME" \
                --arg path "$WORKTREE_PATH" \
                '{timestamp:$ts,name:$name,path:$path,event:"WorktreeCreate"}' \
                >> "$LOG_DIR/worktrees.jsonl" 2>/dev/null || true
        fi
        exit 0
    else
        echo "Failed to create worktree: $WORKTREE_NAME" >&2
        exit 1
    fi
fi

exit 0
