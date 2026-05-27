#!/bin/bash
# auto-save.sh - Auto-commit on agent/task completion
# Hooks: SubagentStop, TaskCompleted
# Purpose: Prevent work loss from resets by other agents
set +e

INPUT="$(cat 2>/dev/null || true)"

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-/workspace}"
cd "$PROJECT_DIR" 2>/dev/null || exit 0

# Skip if not a git repo
git rev-parse --is-inside-work-tree &>/dev/null || exit 0

# Skip if on main/master or detached HEAD (never commit directly)
BRANCH=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")
case "$BRANCH" in main|master|HEAD|"") exit 0 ;; esac

# Skip if no changes
git diff --quiet HEAD 2>/dev/null && git diff --cached --quiet 2>/dev/null && \
  [ -z "$(git ls-files --others --exclude-standard 2>/dev/null)" ] && exit 0

# Build context from input
CONTEXT=""
if [ -n "$INPUT" ] && command -v jq &>/dev/null; then
    AGENT_TYPE=$(printf '%s' "$INPUT" | jq -r '.agent_type // ""' 2>/dev/null || echo "")
    TASK_SUBJECT=$(printf '%s' "$INPUT" | jq -r '.task_subject // ""' 2>/dev/null || echo "")
    TEAMMATE=$(printf '%s' "$INPUT" | jq -r '.teammate_name // ""' 2>/dev/null || echo "")
    if [ -n "$TASK_SUBJECT" ]; then
        CONTEXT="task: ${TASK_SUBJECT:0:60}"
    elif [ -n "$AGENT_TYPE" ]; then
        CONTEXT="agent: $AGENT_TYPE"
    elif [ -n "$TEAMMATE" ]; then
        CONTEXT="teammate: $TEAMMATE"
    fi
fi
[ -z "$CONTEXT" ] && CONTEXT="progress checkpoint"

# Stage all + commit
# --no-verify is opt-in via AUTO_SAVE_SKIP_HOOKS=1 (default: run hooks)
git add -A 2>/dev/null
if [ "${AUTO_SAVE_SKIP_HOOKS:-0}" = "1" ]; then
  git commit --no-verify -m "chore(save): $CONTEXT" 2>/dev/null || true
else
  git commit -m "chore(save): $CONTEXT" 2>/dev/null || true
fi

exit 0
