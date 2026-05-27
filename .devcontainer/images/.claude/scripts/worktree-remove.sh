#!/bin/bash
# ============================================================================
# worktree-remove.sh - Handle worktree removal
# Hook: WorktreeRemove (no matcher, always fires)
# Exit 0 = always (fail-open)
#
# Purpose: Clean up logs and resources when a worktree is removed.
# ============================================================================

set +e

INPUT="$(cat 2>/dev/null || true)"
WORKTREE_PATH=""
if command -v jq &>/dev/null && [ -n "$INPUT" ]; then
    WORKTREE_PATH=$(printf '%s' "$INPUT" | jq -r '.worktree_path // ""' 2>/dev/null || echo "")
fi

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-/workspace}"
TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# Log the removal
LOG_DIR="$PROJECT_DIR/.claude/logs"
mkdir -p "$LOG_DIR" 2>/dev/null || true

if command -v jq &>/dev/null && [ -n "$WORKTREE_PATH" ]; then
    jq -n -c \
        --arg ts "$TIMESTAMP" \
        --arg path "$WORKTREE_PATH" \
        '{timestamp:$ts,path:$path,event:"WorktreeRemove"}' \
        >> "$LOG_DIR/worktrees.jsonl" 2>/dev/null || true
fi

# Actually remove the worktree directory (with safety checks)
# Canonicalize all paths to prevent directory traversal attacks
WORKTREE_REAL=$(realpath -m "$WORKTREE_PATH" 2>/dev/null || echo "")
WORKTREE_BASE_REAL=$(realpath -m "$HOME/.claude/worktrees" 2>/dev/null || echo "")
BUILTIN_BASE_REAL=$(realpath -m "$PROJECT_DIR/.claude/worktrees" 2>/dev/null || echo "")
TMP_BASE_REAL=$(realpath -m "/tmp/claude-worktrees" 2>/dev/null || echo "/tmp/claude-worktrees")

if [ -n "$WORKTREE_REAL" ] && \
   { [[ "$WORKTREE_REAL" == "$WORKTREE_BASE_REAL/"* ]] || \
     [[ "$WORKTREE_REAL" == "$BUILTIN_BASE_REAL/"* ]] || \
     [[ "$WORKTREE_REAL" == "$TMP_BASE_REAL/"* ]]; } && \
   [ -d "$WORKTREE_REAL" ]; then
    # Prune git worktree reference first, then remove directory
    git -C "$PROJECT_DIR" worktree remove "$WORKTREE_REAL" --force 2>/dev/null || \
        rm -rf -- "$WORKTREE_REAL" 2>/dev/null || true
    git -C "$PROJECT_DIR" worktree prune 2>/dev/null || true
fi

exit 0
