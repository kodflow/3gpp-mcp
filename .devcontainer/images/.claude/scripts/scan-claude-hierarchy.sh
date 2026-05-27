#!/bin/bash
# ============================================================================
# scan-claude-hierarchy.sh - Scan CLAUDE.md hierarchy in 1 JSON call
# Usage: scan-claude-hierarchy.sh [project_dir]
# Exit 0 = always (fail-open)
#
# Replaces 4-6 Glob/Read operations in /warmup Phase 1.0
# ============================================================================

set +e  # Fail-open

PROJECT_DIR="${1:-${CLAUDE_PROJECT_DIR:-/workspace}}"

# Require jq for JSON output
if ! command -v jq &>/dev/null; then
    echo '{"error":"jq not installed","files":[],"total_lines":0,"oversized":[]}' >&2
    exit 0
fi

FILES="[]"
TOTAL_LINES=0
OVERSIZED="[]"

while IFS= read -r f; do
    [ -z "$f" ] && continue
    lines=$(wc -l < "$f" 2>/dev/null | tr -d ' ')
    TOTAL_LINES=$((TOTAL_LINES + lines))

    # Calculate depth level relative to project root
    rel_path="${f#"$PROJECT_DIR"/}"
    level=$(echo "$rel_path" | tr -cd '/' | wc -c | tr -d ' ')

    FILES=$(echo "$FILES" | jq --arg p "$f" --arg l "$lines" --arg lv "$level" \
        '. + [{"path": $p, "lines": ($l | tonumber), "level": ($lv | tonumber)}]')

    if [ "$lines" -gt 200 ] 2>/dev/null; then
        OVERSIZED=$(echo "$OVERSIZED" | jq --arg p "$f" '. + [$p]')
    fi
done < <(find "$PROJECT_DIR" -name "CLAUDE.md" -not -path "*/node_modules/*" -not -path "*/.git/*" -not -path "*/.claude/worktrees/*" 2>/dev/null | sort)

jq -n \
    --argjson files "$FILES" \
    --arg total "$TOTAL_LINES" \
    --argjson oversized "$OVERSIZED" \
    '{
        files: $files,
        total_lines: ($total | tonumber),
        oversized: $oversized
    }'
