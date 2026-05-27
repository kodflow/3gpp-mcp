#!/bin/bash
# =============================================================================
# list-team-agents.sh
# =============================================================================
# Deterministic extraction of agent names referenced by team-mode-migrated
# skills. Used by Phase C-minimal to produce the exact agent scope instead
# of a human estimate.
#
# Pure function. No side effects. Output on stdout.
#
# Usage:
#   bash .devcontainer/scripts/list-team-agents.sh [commands_dir]
#
# Default commands_dir: .devcontainer/images/.claude/commands
#
# Output: JSON array of unique agent names on stdout, e.g.
#   ["developer-executor-correctness","developer-executor-security",...]
#
# Exit codes:
#   0 - success (even if empty array)
#   1 - commands_dir not found
# =============================================================================

set -euo pipefail

COMMANDS_DIR="${1:-.devcontainer/images/.claude/commands}"

if [ ! -d "$COMMANDS_DIR" ]; then
    echo "Error: commands directory not found: $COMMANDS_DIR" >&2
    exit 1
fi

# Find skill files that reference the shared team-mode protocol.
# This is the marker of a team-migrated skill.
migrated=$(grep -rlE '@.*shared/team-mode\.md' "$COMMANDS_DIR" 2>/dev/null || true)

if [ -z "$migrated" ]; then
    # No skills migrated yet (expected during Phase A)
    echo "[]"
    exit 0
fi

# Extract agent names from patterns like:
#   "using developer-executor-correctness"
#   "spawn teammate developer-specialist-go"
#   "subagent_type: devops-specialist-aws"
#   "- developer-orchestrator"
# Pattern: <role>-<function>-<specialty> where role ∈ {developer, devops, docs, os}
#
# We use an intentionally generous regex to catch all current naming patterns.
# If a new prefix is added later, update the character class.
echo "$migrated" | while IFS= read -r file; do
    [ -z "$file" ] && continue
    grep -oE '(developer|devops|docs|os)-(orchestrator|executor|specialist|analyzer|commentator)-[a-z][a-z0-9-]*' "$file" 2>/dev/null || true
done | sort -u | jq -R -s -c 'split("\n") | map(select(length > 0))'
