#!/bin/bash
# ============================================================================
# permission-request.sh - Auto-approve safe Bash patterns
# Hook: PermissionRequest (Bash tool)
# Exit 0 = defer to user, output hookSpecificOutput.decision.behavior = auto-approve
# Exit 2 = deny (block)
#
# Purpose: Reduce permission prompts for well-known safe commands.
# ============================================================================

set +e

INPUT="$(cat 2>/dev/null || true)"
if [ -z "$INPUT" ] || ! command -v jq &>/dev/null; then
    exit 0  # Defer to user
fi

TOOL_NAME=$(printf '%s' "$INPUT" | jq -r '.tool_name // ""' 2>/dev/null || echo "")
COMMAND=$(printf '%s' "$INPUT" | jq -r '.tool_input.command // ""' 2>/dev/null || echo "")

# Only handle Bash tool
if [ "$TOOL_NAME" != "Bash" ]; then
    exit 0
fi

# Auto-approve safe read-only and dev-tool patterns
SAFE_PATTERNS=(
    "^git (status|log|diff|branch|show|ls-files|rev-parse|remote)"
    "^ls "
    "^cat "
    "^head "
    "^tail "
    "^wc "
    "^which "
    "^echo "
    "^jq "
    "^yq "
    "^date"
    "^env$"
    "^printenv"
    "^docker (ps|logs|images|compose ps|compose logs)"
    "^tree "
    "^pwd$"
)

for pattern in "${SAFE_PATTERNS[@]}"; do
    if echo "$COMMAND" | grep -qE "$pattern"; then
        printf '{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}'
        exit 0
    fi
done

# Defer to user for anything else
exit 0
