#!/bin/bash
# ============================================================================
# on-stop-learn.sh - Async Stop hook proposing pattern extraction
# Hook: Stop (all matchers)
# Exit 0 = always (fail-open)
#
# Purpose: After a session with enough observations, suggest running /learn.
# ============================================================================

set +e  # Fail-open: never block

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Source shared utilities
# shellcheck source=common.sh
[ -f "$SCRIPT_DIR/common.sh" ] && . "$SCRIPT_DIR/common.sh"

# Gate: full profile only
check_hook_profile "full" || exit 0

# Drain stdin (Stop hooks receive JSON but we don't need it)
cat >/dev/null 2>&1 || true

# Require jq and observations file
command -v jq &>/dev/null || exit 0
OBS_FILE="$HOME/.claude/learning/observations.jsonl"
[ -f "$OBS_FILE" ] || exit 0

SESSION_ID="${CLAUDE_SESSION_ID:-unknown}"

# Count observations for this session (last 50 lines for speed)
COUNT=$(tail -n 50 "$OBS_FILE" 2>/dev/null \
    | jq -r --arg sid "$SESSION_ID" 'select(.sid == $sid) | .sid' 2>/dev/null \
    | wc -l)
COUNT=$((COUNT + 0))  # Ensure numeric

# Only suggest if session had meaningful activity
[ "$COUNT" -lt 20 ] && exit 0

echo "Session had $COUNT observations. Run /learn to extract reusable patterns." >&2

exit 0
